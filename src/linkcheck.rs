//! Link checking: asks a bookmark's address whether it still answers.
//!
//! The server makes the request, so a user could aim it at the server's own network.
//! Two rules prevent that. Only public addresses are contacted, whether the URL names
//! one directly or a host name resolves to one, and every redirect is held to the same
//! rule. The resolver filters the addresses the connection then uses, so the check
//! cannot be undone by a second DNS answer.

use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt;
use std::io;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};
use std::sync::Arc;
use std::time::Duration;

use anyhow::Result;
use reqwest::dns::{Addrs, Name, Resolve, Resolving};
use reqwest::{Client, Method, StatusCode, Url, redirect};
use serde::Deserialize;
use sqlx::{QueryBuilder, Sqlite, SqlitePool};
use tokio::sync::{Semaphore, mpsc};
use tokio::task::JoinSet;

use crate::bookmarks::{LinkCheck, ValidationError};

pub const MAX_CHECK_IDS: usize = 200;

const MAX_REDIRECTS: usize = 5;
const REQUEST_TIMEOUT: Duration = Duration::from_secs(10);
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
// Across all users: a few slow sites must not tie up the server.
const MAX_REQUESTS_IN_FLIGHT: usize = 16;
// Per check, so one user cannot queue ahead of everyone else for the slots above.
const MAX_HOSTS_PER_CHECK: usize = 4;
// Honest about being a robot, and says where to ask about it. The `Mozilla/5.0
// (compatible; ...)` shape is the one crawlers such as Googlebot use.
const USER_AGENT: &str = concat!(
    "Mozilla/5.0 (compatible; bkd-link-checker/",
    env!("CARGO_PKG_VERSION"),
    "; +https://github.com/henry40408/bkd)"
);
// Two timeouts in a row mean the site is not answering. Asking about its other bookmarks
// would only make the check wait 10 seconds for each of them.
const TIMEOUTS_BEFORE_GIVING_UP: u32 = 2;

// A slow site is usually busy, not gone, so a timeout is "unknown" rather than "broken".
const TIMED_OUT: &str = "Timed out";
const COULD_NOT_CONNECT: &str = "Could not connect";
const NOT_PUBLIC: &str = "Address is not public";
const BOT_CHECK: &str = "Blocked by a bot check";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LinkStatus {
    /// Answered with success at the address that was saved.
    Ok,
    /// Answered with success, but only after a redirect to another address.
    Redirected,
    Broken,
    /// The site answered in a way that does not say whether the page exists, such as
    /// a refusal for robots, or the address was not contacted.
    Unknown,
}

impl LinkStatus {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Ok => "ok",
            Self::Redirected => "redirected",
            Self::Broken => "broken",
            Self::Unknown => "unknown",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Verdict {
    pub status: LinkStatus,
    /// Short, for people: `HTTP 404`, or the address a redirect ended at.
    pub detail: String,
}

impl Verdict {
    fn new(status: LinkStatus, detail: impl Into<String>) -> Self {
        Self {
            status,
            detail: detail.into(),
        }
    }
}

// --- Which addresses may be contacted ---

#[derive(Debug, Clone, Copy)]
struct Guard {
    /// Only tests set this, so they can use a server on this machine.
    allow_loopback: bool,
}

impl Guard {
    fn allows(self, ip: IpAddr) -> bool {
        (self.allow_loopback && ip.is_loopback()) || is_public(ip)
    }

    /// Checks the scheme, and the host when the URL gives an IP address instead of a
    /// name. A name is checked later, by the resolver.
    fn allows_url(self, url: &Url) -> bool {
        if !matches!(url.scheme(), "http" | "https") {
            return false;
        }
        let Some(host) = url.host_str() else {
            return false;
        };
        // `host_str` writes an IPv6 address in brackets. The URL parser has already turned
        // spellings such as `2130706433` and `0x7f.1` into plain dotted form.
        match host
            .trim_start_matches('[')
            .trim_end_matches(']')
            .parse::<IpAddr>()
        {
            Ok(ip) => self.allows(ip),
            Err(_) => true,
        }
    }
}

fn is_public(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(v4) => is_public_v4(v4),
        // `::ffff:10.0.0.1` reaches 10.0.0.1, so it is judged as that address.
        IpAddr::V6(v6) => v6
            .to_ipv4_mapped()
            .map_or_else(|| is_public_v6(v6), is_public_v4),
    }
}

fn is_public_v4(ip: Ipv4Addr) -> bool {
    let [a, b, c, _] = ip.octets();
    let shared_address_space = a == 100 && (64..128).contains(&b);
    let protocol_assignments = a == 192 && b == 0 && c == 0;
    let benchmarking = a == 198 && (b == 18 || b == 19);
    let reserved = a >= 240;
    let this_network = a == 0;
    let relay_anycast = a == 192 && b == 88 && c == 99;
    !(ip.is_unspecified()
        || ip.is_loopback()
        || ip.is_private()
        || ip.is_link_local()
        || ip.is_multicast()
        || ip.is_documentation()
        || shared_address_space
        || protocol_assignments
        || benchmarking
        || this_network
        || relay_anycast
        || reserved)
}

fn is_public_v6(ip: Ipv6Addr) -> bool {
    let s = ip.segments();
    // Only 2000::/3 is global unicast. Everything else, assigned or not, is refused,
    // which covers `::`, `::1`, unique-local, link-local, site-local, multicast, NAT64
    // and the IPv4-compatible forms.
    let global_unicast = s[0] & 0xe000 == 0x2000;
    let teredo_and_special = s[0] == 0x2001 && s[1] < 0x0200;
    let documentation = (s[0] == 0x2001 && s[1] == 0x0db8) || (s[0] == 0x3fff && s[1] < 0x1000);
    // This one embeds an IPv4 address that a gateway may forward to.
    let six_to_four = s[0] == 0x2002;
    global_unicast && !(teredo_and_special || documentation || six_to_four)
}

#[derive(Debug)]
struct BlockedAddress;

impl fmt::Display for BlockedAddress {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("the address is not public")
    }
}

impl Error for BlockedAddress {}

#[derive(Debug)]
struct TooManyRedirects;

impl fmt::Display for TooManyRedirects {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("too many redirects")
    }
}

impl Error for TooManyRedirects {}

/// Resolves a host name and keeps only the public addresses.
struct GuardedResolver {
    guard: Guard,
}

impl Resolve for GuardedResolver {
    fn resolve(&self, name: Name) -> Resolving {
        let guard = self.guard;
        Box::pin(async move {
            let found: Vec<SocketAddr> =
                tokio::net::lookup_host((name.as_str(), 0)).await?.collect();
            if found.is_empty() {
                return Err(
                    io::Error::new(io::ErrorKind::NotFound, "no address for the host").into(),
                );
            }
            let allowed: Vec<SocketAddr> =
                found.into_iter().filter(|a| guard.allows(a.ip())).collect();
            if allowed.is_empty() {
                return Err(BlockedAddress.into());
            }
            Ok(Box::new(allowed.into_iter()) as Addrs)
        })
    }
}

fn caused_by<E: Error + 'static>(error: &reqwest::Error) -> bool {
    let mut source = error.source();
    while let Some(cause) = source {
        if cause.is::<E>() {
            return true;
        }
        source = cause.source();
    }
    false
}

// --- Asking the site ---

struct Answer {
    status: StatusCode,
    final_url: Url,
    /// The site put a bot check in front of the page (Cloudflare's `cf-mitigated`), so
    /// the status says nothing about the page.
    challenged: bool,
    retry_after: Option<Duration>,
}

impl Answer {
    /// "Too many requests" or "unavailable": the site wants to be left alone for a while.
    fn asks_to_wait(&self) -> bool {
        matches!(self.status.as_u16(), 429 | 503)
    }

    /// Asking again with GET would get the same refusal, so it only adds load.
    fn is_final(&self) -> bool {
        self.status.is_success() || self.challenged || self.asks_to_wait()
    }
}

/// A site that should be left alone for a while. `retry_after` is what the site asked
/// for, when it said; how long to really wait is for the caller to decide.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Backoff {
    pub host: String,
    pub retry_after: Option<Duration>,
}

/// A verdict, and whether the site should be left alone.
pub struct Inspection {
    pub verdict: Verdict,
    pub backoff: Option<Backoff>,
    timed_out: bool,
    /// A site replied, as opposed to a timeout or a failed connection.
    answered: bool,
}

#[derive(Clone)]
pub struct Checker {
    client: Client,
    guard: Guard,
    in_flight: Arc<Semaphore>,
    /// For one bookmark, HEAD and GET together.
    budget: Duration,
}

impl Checker {
    pub fn public_only() -> Result<Self> {
        Self::with_guard(
            Guard {
                allow_loopback: false,
            },
            REQUEST_TIMEOUT,
        )
    }

    #[cfg(test)]
    pub fn allowing_loopback() -> Result<Self> {
        Self::allowing_loopback_with_timeout(REQUEST_TIMEOUT)
    }

    /// For tests of a site that does not answer, which cannot wait 10 seconds.
    #[cfg(test)]
    pub fn allowing_loopback_with_timeout(timeout: Duration) -> Result<Self> {
        Self::with_guard(
            Guard {
                allow_loopback: true,
            },
            timeout,
        )
    }

    fn with_guard(guard: Guard, timeout: Duration) -> Result<Self> {
        let client = Client::builder()
            .user_agent(USER_AGENT)
            .no_proxy()
            // Every host is visited once, so a pooled connection is only an open file descriptor.
            .pool_max_idle_per_host(0)
            .timeout(timeout)
            .connect_timeout(CONNECT_TIMEOUT)
            .dns_resolver(Arc::new(GuardedResolver { guard }))
            .redirect(redirect::Policy::custom(move |attempt| {
                if attempt.previous().len() > MAX_REDIRECTS {
                    return attempt.error(TooManyRedirects);
                }
                if !guard.allows_url(attempt.url()) {
                    return attempt.error(BlockedAddress);
                }
                attempt.follow()
            }))
            .build()?;
        Ok(Self {
            client,
            guard,
            in_flight: Arc::new(Semaphore::new(MAX_REQUESTS_IN_FLIGHT)),
            budget: timeout,
        })
    }

    pub async fn inspect(&self, address: &str) -> Inspection {
        let Ok(url) = Url::parse(address) else {
            return Inspection::without_backoff(Verdict::new(
                LinkStatus::Unknown,
                "Not a valid address",
            ));
        };
        if !self.guard.allows_url(&url) {
            return Inspection::without_backoff(Verdict::new(LinkStatus::Unknown, NOT_PUBLIC));
        }
        // The semaphore is never closed, so acquiring cannot fail.
        let _permit = self
            .in_flight
            .acquire()
            .await
            .expect("semaphore is never closed");
        // HEAD and the GET after it share one budget, so a slow site costs 10 seconds, not 20.
        match tokio::time::timeout(self.budget, self.request(&url)).await {
            Ok(inspection) => inspection,
            Err(_) => Inspection::timed_out(),
        }
    }

    async fn request(&self, url: &Url) -> Inspection {
        let answer = match self.ask(Method::HEAD, url).await {
            Ok(answer) if answer.is_final() => return Inspection::from_answer(url, &answer),
            // Waiting for the same host again would only repeat the delay.
            Err(error) if error.is_timeout() || caused_by::<BlockedAddress>(&error) => {
                return Inspection::from_error(&error);
            }
            // Many servers refuse or mishandle HEAD, so anything else is confirmed with GET.
            _ => self.ask(Method::GET, url).await,
        };
        match answer {
            Ok(answer) => Inspection::from_answer(url, &answer),
            Err(error) => Inspection::from_error(&error),
        }
    }

    /// Reads only the status line and headers. Dropping the response closes the
    /// connection, so a GET never downloads the page.
    async fn ask(&self, method: Method, url: &Url) -> Result<Answer, reqwest::Error> {
        let response = self.client.request(method, url.clone()).send().await?;
        let headers = response.headers();
        let challenged = headers
            .get("cf-mitigated")
            .is_some_and(|value| value.as_bytes().eq_ignore_ascii_case(b"challenge"));
        // Only the seconds form; a date is rare, and the default wait is a safe answer.
        let retry_after = headers
            .get(reqwest::header::RETRY_AFTER)
            .and_then(|value| value.to_str().ok())
            .and_then(|text| text.trim().parse::<u64>().ok())
            .map(Duration::from_secs);
        Ok(Answer {
            status: response.status(),
            final_url: response.url().clone(),
            challenged,
            retry_after,
        })
    }

    /// Starts checking every target and hands back the results as each one finishes.
    /// Targets on one host go one after another, so a batch of bookmarks from one
    /// site is not a burst of requests to it. A host that asks to be left alone
    /// (429, 503) or does not answer twice in a row is not asked again in this check;
    /// its other targets come back as skipped.
    pub fn start(&self, targets: Vec<Target>) -> RunningCheck {
        self.start_paced(targets, Duration::ZERO)
    }

    /// Like [`Checker::start`], and waits `pause` between two requests to one host.
    pub fn start_paced(&self, targets: Vec<Target>, pause: Duration) -> RunningCheck {
        let mut by_host: HashMap<String, Vec<Target>> = HashMap::new();
        for target in targets {
            by_host
                .entry(host_of(&target.url))
                .or_default()
                .push(target);
        }
        let (sender, receiver) = mpsc::unbounded_channel();
        let hosts_at_once = Arc::new(Semaphore::new(MAX_HOSTS_PER_CHECK));
        let mut tasks = JoinSet::new();
        for (host, group) in by_host {
            let checker = self.clone();
            let sender = sender.clone();
            let hosts_at_once = hosts_at_once.clone();
            tasks.spawn(async move {
                let Ok(_turn) = hosts_at_once.acquire().await else {
                    return;
                };
                checker.check_host(host, group, pause, sender).await;
            });
        }
        RunningCheck {
            receiver,
            _tasks: tasks,
        }
    }

    /// One host's targets, one after another, until the host asks to be left alone or
    /// stops answering.
    async fn check_host(
        &self,
        host: String,
        group: Vec<Target>,
        pause: Duration,
        sender: mpsc::UnboundedSender<Outcome>,
    ) {
        let mut targets = group.into_iter();
        let mut timeouts_in_a_row = 0;
        let mut first = true;
        while let Some(target) = targets.next() {
            if !first {
                tokio::time::sleep(pause).await;
            }
            first = false;
            let inspection = self.inspect(&target.url).await;
            timeouts_in_a_row = if inspection.timed_out {
                timeouts_in_a_row + 1
            } else {
                0
            };
            let gives_up = timeouts_in_a_row >= TIMEOUTS_BEFORE_GIVING_UP;
            let mut backoff = inspection.backoff;
            // A redirect can end at another site; only an answer from this one stops this group.
            let asked_to_wait = backoff.as_ref().is_some_and(|backoff| backoff.host == host);
            if gives_up && backoff.is_none() {
                backoff = Some(Backoff {
                    host: host.clone(),
                    retry_after: None,
                });
            }
            let _ = sender.send(Outcome::Checked(Checked {
                target,
                verdict: inspection.verdict,
                backoff,
                answered: inspection.answered,
            }));
            let reason = match (asked_to_wait, gives_up) {
                (true, _) => SkipReason::SlowDown,
                (false, true) => SkipReason::NotAnswering,
                (false, false) => continue,
            };
            for rest in targets {
                let _ = sender.send(Outcome::Skipped(Skipped {
                    target: rest,
                    reason,
                    host: host.clone(),
                }));
            }
            return;
        }
    }
}

/// A check in progress. Dropping it stops the requests that have not finished, so a
/// client that goes away does not leave the server working for nobody.
pub struct RunningCheck {
    receiver: mpsc::UnboundedReceiver<Outcome>,
    _tasks: JoinSet<()>,
}

impl RunningCheck {
    /// The next result, or `None` once every target has been dealt with.
    pub async fn next(&mut self) -> Option<Outcome> {
        self.receiver.recv().await
    }
}

/// What became of one target.
#[derive(Debug)]
pub enum Outcome {
    Checked(Checked),
    /// Not asked, because its site asked to be left alone or stopped answering.
    Skipped(Skipped),
}

#[derive(Debug)]
pub struct Skipped {
    pub target: Target,
    pub reason: SkipReason,
    pub host: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SkipReason {
    SlowDown,
    NotAnswering,
}

impl SkipReason {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::SlowDown => "slow_down",
            Self::NotAnswering => "not_answering",
        }
    }
}

/// The host that requests are spaced by. `www.example.com` and `example.com` are one
/// site, so they count as one.
pub fn host_of(address: &str) -> String {
    let host = Url::parse(address)
        .ok()
        .and_then(|url| url.host_str().map(str::to_lowercase))
        .unwrap_or_default();
    match host.strip_prefix("www.") {
        Some(rest) => rest.to_owned(),
        None => host,
    }
}

fn without_fragment(url: &Url) -> Url {
    let mut url = url.clone();
    url.set_fragment(None);
    url
}

impl Inspection {
    fn without_backoff(verdict: Verdict) -> Self {
        Self {
            verdict,
            backoff: None,
            timed_out: false,
            answered: false,
        }
    }

    fn timed_out() -> Self {
        Self {
            timed_out: true,
            ..Self::without_backoff(Verdict::new(LinkStatus::Unknown, TIMED_OUT))
        }
    }

    fn from_error(error: &reqwest::Error) -> Self {
        Self {
            timed_out: error.is_timeout(),
            ..Self::without_backoff(verdict_for_error(error))
        }
    }

    fn from_answer(requested: &Url, answer: &Answer) -> Self {
        // The site that answered, which after a redirect is not the one that was asked.
        let backoff = answer.asks_to_wait().then(|| Backoff {
            host: host_of(answer.final_url.as_str()),
            retry_after: answer.retry_after,
        });
        Self {
            verdict: verdict_for(requested, answer),
            backoff,
            timed_out: false,
            answered: true,
        }
    }
}

fn verdict_for(requested: &Url, answer: &Answer) -> Verdict {
    if answer.challenged {
        return Verdict::new(LinkStatus::Unknown, BOT_CHECK);
    }
    let status = answer.status;
    if status.is_success() {
        if without_fragment(requested) == without_fragment(&answer.final_url) {
            return Verdict::new(LinkStatus::Ok, "");
        }
        return Verdict::new(LinkStatus::Redirected, answer.final_url.as_str());
    }
    let detail = format!("HTTP {}", status.as_u16());
    // These say "not for robots" or "not now", not "gone". 999 is LinkedIn's refusal.
    let inconclusive = matches!(status.as_u16(), 401 | 403 | 429 | 503 | 999)
        || !status.is_client_error() && !status.is_server_error();
    if inconclusive {
        return Verdict::new(LinkStatus::Unknown, detail);
    }
    Verdict::new(LinkStatus::Broken, detail)
}

fn verdict_for_error(error: &reqwest::Error) -> Verdict {
    if caused_by::<BlockedAddress>(error) {
        return Verdict::new(LinkStatus::Unknown, NOT_PUBLIC);
    }
    if caused_by::<TooManyRedirects>(error) {
        return Verdict::new(LinkStatus::Broken, "Too many redirects");
    }
    if error.is_timeout() {
        return Verdict::new(LinkStatus::Unknown, TIMED_OUT);
    }
    if error.is_redirect() {
        return Verdict::new(LinkStatus::Broken, "Bad redirect");
    }
    Verdict::new(LinkStatus::Broken, COULD_NOT_CONNECT)
}

// --- The request and the stored result ---

/// What a client sends. It becomes a [`ValidCheck`] only through `validate`.
#[derive(Debug, Deserialize)]
pub struct CheckInput {
    pub ids: Vec<i64>,
}

#[derive(Debug, PartialEq, Eq)]
pub struct ValidCheck {
    ids: Vec<i64>,
}

impl CheckInput {
    pub fn validate(self) -> Result<ValidCheck, ValidationError> {
        let mut seen = HashSet::new();
        let ids: Vec<i64> = self.ids.into_iter().filter(|id| seen.insert(*id)).collect();
        if ids.is_empty() {
            return Err(ValidationError("ids must not be empty"));
        }
        if ids.len() > MAX_CHECK_IDS {
            return Err(ValidationError("too many ids"));
        }
        Ok(ValidCheck { ids })
    }
}

/// Users who have a check running. A user gets one at a time, so a few accounts cannot
/// take every request slot from everyone else.
#[derive(Clone, Default)]
pub struct ActiveChecks(Arc<std::sync::Mutex<HashSet<i64>>>);

/// Held while a user's check runs; dropping it lets the user start another.
pub struct ActiveCheck {
    users: ActiveChecks,
    user_id: i64,
}

impl ActiveChecks {
    pub fn start(&self, user_id: i64) -> Option<ActiveCheck> {
        let first = self
            .0
            .lock()
            .expect("active checks lock poisoned")
            .insert(user_id);
        first.then(|| ActiveCheck {
            users: self.clone(),
            user_id,
        })
    }
}

impl Drop for ActiveCheck {
    fn drop(&mut self) {
        self.users
            .0
            .lock()
            .expect("active checks lock poisoned")
            .remove(&self.user_id);
    }
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct Target {
    pub id: i64,
    pub url: String,
}

#[derive(Debug)]
pub struct Checked {
    pub target: Target,
    pub verdict: Verdict,
    /// Set when a site should be left alone for a while.
    pub backoff: Option<Backoff>,
    /// A site replied. A timeout or a failed connection is not an answer.
    pub answered: bool,
}

/// The bookmarks to check, or `None` when an id is missing or belongs to someone else.
pub async fn targets(
    pool: &SqlitePool,
    user_id: i64,
    request: &ValidCheck,
) -> Result<Option<Vec<Target>>> {
    let mut query = QueryBuilder::<Sqlite>::new("SELECT id, url FROM bookmarks WHERE user_id = ");
    query.push_bind(user_id);
    query.push(" AND id IN (");
    let mut ids = query.separated(", ");
    for id in &request.ids {
        ids.push_bind(*id);
    }
    query.push(")");
    let found: Vec<Target> = query.build_query_as().fetch_all(pool).await?;
    Ok((found.len() == request.ids.len()).then_some(found))
}

/// Stores the verdicts and returns what was stored, keyed by bookmark id. A bookmark
/// whose URL was edited during the check is skipped, because the verdict describes
/// the old address.
pub async fn store(
    pool: &SqlitePool,
    user_id: i64,
    checked: &[Checked],
    now: i64,
) -> Result<Vec<(i64, LinkCheck)>> {
    let mut transaction = pool.begin().await?;
    let mut stored = Vec::with_capacity(checked.len());
    for Checked {
        target, verdict, ..
    } in checked
    {
        let updated = sqlx::query(
            "UPDATE bookmarks SET link_status = ?1, link_checked_at = ?2, link_detail = ?3
             WHERE id = ?4 AND user_id = ?5 AND url = ?6",
        )
        .bind(verdict.status.as_str())
        .bind(now)
        .bind(&verdict.detail)
        .bind(target.id)
        .bind(user_id)
        .bind(&target.url)
        .execute(&mut *transaction)
        .await?
        .rows_affected();
        if updated == 1 {
            stored.push((
                target.id,
                LinkCheck {
                    status: verdict.status.as_str().to_owned(),
                    checked_at: now,
                    detail: verdict.detail.clone(),
                },
            ));
        }
    }
    transaction.commit().await?;
    Ok(stored)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ip(text: &str) -> IpAddr {
        text.parse().unwrap()
    }

    #[test]
    fn private_and_special_addresses_are_not_public() {
        for text in [
            "127.0.0.1",
            "10.1.2.3",
            "172.16.0.1",
            "172.31.255.255",
            "192.168.1.1",
            "169.254.169.254",
            "0.0.0.0",
            "100.64.0.1",
            "100.127.255.255",
            "198.18.0.1",
            "192.0.0.8",
            "192.0.2.1",
            "224.0.0.1",
            "240.0.0.1",
            "255.255.255.255",
            "::",
            "::1",
            "::10.0.0.1",
            "fc00::1",
            "fd12::1",
            "fe80::1",
            "ff02::1",
            "2001:db8::1",
            "2002:c0a8:0101::1",
            "64:ff9b::a00:1",
            "::ffff:10.0.0.1",
            "::ffff:127.0.0.1",
        ] {
            assert!(!is_public(ip(text)), "{text} must not be public");
        }
    }

    #[test]
    fn reserved_ranges_that_are_easy_to_miss_are_not_public() {
        for text in [
            "0.1.2.3",
            "192.88.99.1",
            "::ffff:0:7f00:1",
            "100::1",
            "fec0::1",
            "2001::1",
            "3fff::1",
            "5f00::1",
        ] {
            assert!(!is_public(ip(text)), "{text} must not be public");
        }
    }

    #[test]
    fn ordinary_addresses_are_public() {
        for text in [
            "8.8.8.8",
            "1.1.1.1",
            "93.184.216.34",
            "172.32.0.1",
            "100.63.255.255",
            "100.128.0.1",
            "198.20.0.1",
            "2606:4700:4700::1111",
            "::ffff:8.8.8.8",
        ] {
            assert!(is_public(ip(text)), "{text} must be public");
        }
    }

    #[test]
    fn a_url_that_gives_a_private_address_or_a_foreign_scheme_is_refused() {
        let guard = Guard {
            allow_loopback: false,
        };
        // A host name such as `localhost` passes here. The resolver refuses it later.
        for refused in [
            "http://127.0.0.1/",
            "http://[::1]/",
            "http://10.0.0.5/x",
            "http://2130706433/",
            "http://0x7f.1/",
            "http://169.254.169.254/latest",
            "http://[::ffff:10.0.0.1]/",
            "ftp://example.test/",
            "file:///etc/passwd",
        ] {
            assert!(
                !guard.allows_url(&Url::parse(refused).unwrap()),
                "{refused}"
            );
        }
        assert!(guard.allows_url(&Url::parse("https://example.test/").unwrap()));
        assert!(guard.allows_url(&Url::parse("http://8.8.8.8/").unwrap()));
    }

    fn answer(status: u16, final_url: &str) -> Answer {
        Answer {
            status: StatusCode::from_u16(status).unwrap(),
            final_url: Url::parse(final_url).unwrap(),
            challenged: false,
            retry_after: None,
        }
    }

    #[test]
    fn statuses_are_sorted_into_the_four_outcomes() {
        let url = Url::parse("https://a.test/page#top").unwrap();
        let verdict = |status, final_url| verdict_for(&url, &answer(status, final_url));
        assert_eq!(verdict(200, "https://a.test/page").status, LinkStatus::Ok);
        let moved = verdict(200, "https://a.test/new");
        assert_eq!(
            (moved.status, moved.detail.as_str()),
            (LinkStatus::Redirected, "https://a.test/new")
        );
        for broken in [404, 410, 400, 500] {
            let found = verdict(broken, "https://a.test/page");
            assert_eq!(
                (found.status, found.detail),
                (LinkStatus::Broken, format!("HTTP {broken}"))
            );
        }
        for unknown in [401, 403, 429, 503, 999, 304] {
            assert_eq!(
                verdict(unknown, "https://a.test/page").status,
                LinkStatus::Unknown,
                "{unknown}"
            );
        }
    }

    #[test]
    fn a_user_has_one_check_at_a_time() {
        let active = ActiveChecks::default();
        let first = active.start(1);
        assert!(first.is_some());
        assert!(active.start(1).is_none());
        assert!(active.start(2).is_some(), "other users are not affected");
        drop(first);
        assert!(active.start(1).is_some(), "finishing frees the slot");
    }

    #[test]
    fn a_batch_asks_for_between_one_and_the_maximum_distinct_ids() {
        let ask = |ids: Vec<i64>| CheckInput { ids }.validate();
        assert!(ask(vec![]).is_err());
        assert!(ask((1..=201).collect()).is_err());
        assert_eq!(ask((1..=200).collect()).unwrap().ids.len(), 200);
        assert_eq!(ask(vec![3, 3, 4]).unwrap().ids, [3, 4]);
    }
}
