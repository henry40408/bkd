//! The background link check: on a timer, checks the bookmarks of users who turned it on.
//!
//! It uses the same checker as the "Check links" button, so the same rules keep it off
//! private addresses. On top of that it is gentle: a bounded batch per round, one
//! request at a time per site with a pause between, and a site that asks to be left
//! alone (429, 503) or stops answering is skipped for a while. The wait is what the site
//! asked for, or else doubles with each time in a row and starts over after an answer.

use std::collections::HashMap;
use std::time::{Duration, Instant};

use anyhow::Result;
use sqlx::SqlitePool;
use tokio::time::MissedTickBehavior;

use crate::auth::unix_now;
use crate::linkcheck::{Backoff, Checker, Outcome, Target, host_of, store};
use crate::users;

/// A result younger than this is not asked for again.
const RECHECK_AFTER_SECONDS: i64 = 7 * 24 * 60 * 60;

/// Rows read per bookmark wanted, so bookmarks of sites that are cooling down can be skipped.
const OVERFETCH: usize = 5;

/// Between two requests to one site. Longer than the manual check, which is quick on purpose.
const SAME_HOST_PAUSE: Duration = Duration::from_millis(1500);

/// The first wait for a site that did not say how long. Each time in a row doubles it.
const BASE_COOLDOWN: Duration = Duration::from_secs(60);

/// Also the longest a site can ask for: a `Retry-After` of a week is not believed.
const MAX_COOLDOWN: Duration = Duration::from_secs(24 * 60 * 60);

pub struct Schedule {
    /// Time between the starts of two rounds.
    pub interval: Duration,
    /// Bookmarks checked per round, all users together.
    pub batch: usize,
}

/// Runs rounds until the task is aborted.
pub async fn run(pool: SqlitePool, checker: Checker, schedule: Schedule) {
    let mut ticker = tokio::time::interval(schedule.interval);
    // A slow round must not be followed by a burst of rounds to catch up.
    ticker.set_missed_tick_behavior(MissedTickBehavior::Delay);
    let mut worker = Worker {
        pool,
        checker,
        batch: schedule.batch,
        cooldowns: HashMap::new(),
    };
    loop {
        ticker.tick().await;
        match worker.run_round().await {
            Ok(0) => {}
            Ok(count) => tracing::info!(count, "background link check finished a round"),
            Err(error) => tracing::error!("background link check failed: {error:#}"),
        }
    }
}

struct Worker {
    pool: SqlitePool,
    checker: Checker,
    batch: usize,
    cooldowns: HashMap<String, HostCooldown>,
}

/// What is known about a site that asked to be left alone.
#[derive(Debug, Clone, Copy)]
struct HostCooldown {
    /// Times in a row it asked. Starts over when the site answers normally.
    strikes: u32,
    until: Instant,
}

/// How long to leave a site alone after its `strikes`th request in a row to be.
fn cooldown_for(strikes: u32, retry_after: Option<Duration>) -> Duration {
    let doublings = strikes.saturating_sub(1).min(20);
    let wait = retry_after.unwrap_or(BASE_COOLDOWN * 2u32.pow(doublings));
    wait.min(MAX_COOLDOWN)
}

#[derive(Debug, sqlx::FromRow)]
struct Due {
    id: i64,
    user_id: i64,
    url: String,
}

impl Worker {
    /// Checks the next batch and returns how many bookmarks got a result.
    async fn run_round(&mut self) -> Result<usize> {
        let due = self.due_now().await?;
        if due.is_empty() {
            return Ok(0);
        }
        let owners: HashMap<i64, i64> = due.iter().map(|due| (due.id, due.user_id)).collect();
        let targets = due
            .into_iter()
            .map(|due| Target {
                id: due.id,
                url: due.url,
            })
            .collect();
        let mut running = self.checker.start_paced(targets, SAME_HOST_PAUSE);
        let mut stored = 0;
        while let Some(outcome) = running.next().await {
            // Skipped targets stay due, and a later round picks them up.
            let Outcome::Checked(checked) = outcome else {
                continue;
            };
            match checked.backoff.clone() {
                Some(backoff) => self.leave_alone(backoff),
                // Only a reply ends the waiting; a timeout says nothing about a site's mood.
                None if checked.answered => {
                    self.cooldowns.remove(&host_of(&checked.target.url));
                }
                None => {}
            }
            let user_id = owners[&checked.target.id];
            // The user may have opted out, or been disabled, since the round began.
            if !users::wants_background_check(&self.pool, user_id).await? {
                continue;
            }
            stored += store(&self.pool, user_id, &[checked], unix_now())
                .await?
                .len();
        }
        Ok(stored)
    }

    fn leave_alone(&mut self, backoff: Backoff) {
        let cooldown = self
            .cooldowns
            .entry(backoff.host.clone())
            .or_insert_with(|| HostCooldown {
                strikes: 0,
                until: Instant::now(),
            });
        cooldown.strikes = cooldown.strikes.saturating_add(1);
        let wait = cooldown_for(cooldown.strikes, backoff.retry_after);
        cooldown.until = Instant::now() + wait;
        tracing::info!(
            host = %backoff.host,
            seconds = wait.as_secs(),
            "leaving a site alone for a while"
        );
    }

    fn is_cooling(&self, host: &str) -> bool {
        self.cooldowns
            .get(host)
            .is_some_and(|cooldown| cooldown.until > Instant::now())
    }

    /// The bookmarks to check now, skipping sites that are cooling down.
    async fn due_now(&mut self) -> Result<Vec<Due>> {
        // The cooling sites are known only after the query, so read extra rows: a site
        // that fills the front of the line must not leave the batch empty.
        let wanted = self.batch.saturating_mul(OVERFETCH);
        let due = due_bookmarks(&self.pool, unix_now(), wanted).await?;
        Ok(due
            .into_iter()
            .filter(|due| !self.is_cooling(&host_of(&due.url)))
            .take(self.batch)
            .collect())
    }
}

/// Never-checked bookmarks first, then the longest unchecked. Users take turns, so one
/// person with thousands of bookmarks does not use up every round.
async fn due_bookmarks(pool: &SqlitePool, now: i64, limit: usize) -> Result<Vec<Due>> {
    let limit = i64::try_from(limit)?;
    let due = sqlx::query_as(
        "SELECT id, user_id, url FROM (
             SELECT b.id, b.user_id, b.url, b.link_checked_at,
                    ROW_NUMBER() OVER (
                        PARTITION BY b.user_id
                        ORDER BY COALESCE(b.link_checked_at, 0), b.id
                    ) AS turn
             FROM bookmarks b
             JOIN users u ON u.id = b.user_id
             WHERE u.link_check_enabled = 1
               AND u.disabled_at IS NULL
               AND (b.link_checked_at IS NULL OR b.link_checked_at < ?1)
         )
         ORDER BY turn, COALESCE(link_checked_at, 0), id
         LIMIT ?2",
    )
    .bind(now - RECHECK_AFTER_SECONDS)
    .bind(limit)
    .fetch_all(pool)
    .await?;
    Ok(due)
}

#[cfg(test)]
mod tests {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    use super::*;
    use crate::db::test_support::{insert_user, test_pool};

    async fn add_bookmark(
        pool: &SqlitePool,
        user_id: i64,
        url: &str,
        checked_at: Option<i64>,
    ) -> i64 {
        sqlx::query_scalar(
            "INSERT INTO bookmarks (user_id, url, title, created_at, updated_at, link_checked_at)
             VALUES (?1, ?2, 't', 0, 0, ?3) RETURNING id",
        )
        .bind(user_id)
        .bind(url)
        .bind(checked_at)
        .fetch_one(pool)
        .await
        .unwrap()
    }

    async fn opt_in(pool: &SqlitePool, user_id: i64) {
        crate::users::set_link_check_enabled(pool, user_id, true)
            .await
            .unwrap();
    }

    fn ids(due: &[Due]) -> Vec<i64> {
        due.iter().map(|due| due.id).collect()
    }

    const NOW: i64 = 10_000_000;

    #[tokio::test]
    async fn only_users_who_opted_in_and_are_active_are_checked() {
        let (_dir, pool) = test_pool().await;
        let off = insert_user(&pool, "quiet").await.unwrap();
        let on = insert_user(&pool, "opted").await.unwrap();
        let disabled = insert_user(&pool, "gone").await.unwrap();
        opt_in(&pool, on).await;
        opt_in(&pool, disabled).await;
        sqlx::query("UPDATE users SET disabled_at = 1 WHERE id = ?1")
            .bind(disabled)
            .execute(&pool)
            .await
            .unwrap();
        add_bookmark(&pool, off, "https://a.test/", None).await;
        add_bookmark(&pool, disabled, "https://b.test/", None).await;
        let wanted = add_bookmark(&pool, on, "https://c.test/", None).await;

        let due = due_bookmarks(&pool, NOW, 10).await.unwrap();
        assert_eq!(ids(&due), [wanted]);
    }

    #[tokio::test]
    async fn a_recent_result_is_left_alone_until_a_week_has_passed() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "mei").await.unwrap();
        opt_in(&pool, user).await;
        let fresh = NOW - RECHECK_AFTER_SECONDS + 1;
        let stale = NOW - RECHECK_AFTER_SECONDS - 1;
        add_bookmark(&pool, user, "https://fresh.test/", Some(fresh)).await;
        let old = add_bookmark(&pool, user, "https://stale.test/", Some(stale)).await;

        let due = due_bookmarks(&pool, NOW, 10).await.unwrap();
        assert_eq!(ids(&due), [old]);
    }

    #[tokio::test]
    async fn never_checked_goes_first_then_the_longest_unchecked() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "mei").await.unwrap();
        opt_in(&pool, user).await;
        let older = add_bookmark(&pool, user, "https://a.test/", Some(100)).await;
        let oldest = add_bookmark(&pool, user, "https://b.test/", Some(50)).await;
        let never = add_bookmark(&pool, user, "https://c.test/", None).await;

        let due = due_bookmarks(&pool, NOW, 10).await.unwrap();
        assert_eq!(ids(&due), [never, oldest, older]);
    }

    #[tokio::test]
    async fn users_take_turns_and_the_batch_is_a_limit() {
        let (_dir, pool) = test_pool().await;
        let big = insert_user(&pool, "big").await.unwrap();
        let small = insert_user(&pool, "small").await.unwrap();
        opt_in(&pool, big).await;
        opt_in(&pool, small).await;
        let mut big_ids = Vec::new();
        for n in 0..5 {
            big_ids.push(add_bookmark(&pool, big, &format!("https://big{n}.test/"), None).await);
        }
        let only = add_bookmark(&pool, small, "https://small.test/", None).await;

        let due = due_bookmarks(&pool, NOW, 3).await.unwrap();
        assert_eq!(due.len(), 3);
        assert!(
            ids(&due).contains(&only),
            "the small account is not starved"
        );
        assert!(ids(&due).contains(&big_ids[0]));
    }

    #[test]
    fn a_site_that_says_nothing_is_left_alone_for_twice_as_long_each_time() {
        let waits: Vec<u64> = (1..=5)
            .map(|strikes| cooldown_for(strikes, None).as_secs())
            .collect();
        assert_eq!(waits, [60, 120, 240, 480, 960]);
        assert_eq!(
            cooldown_for(100, None),
            MAX_COOLDOWN,
            "capped, and no overflow"
        );
    }

    #[test]
    fn what_a_site_asks_for_is_used_but_never_more_than_a_day() {
        let asked = |seconds| cooldown_for(3, Some(Duration::from_secs(seconds)));
        assert_eq!(asked(30), Duration::from_secs(30));
        assert_eq!(asked(7 * 24 * 3600), MAX_COOLDOWN);
    }

    #[tokio::test]
    async fn an_answer_starts_the_waiting_over() {
        let (_dir, pool) = test_pool().await;
        let mut worker = worker(pool, Checker::public_only().unwrap());
        let ask = || Backoff {
            host: "slow.test".to_owned(),
            retry_after: None,
        };
        worker.leave_alone(ask());
        worker.leave_alone(ask());
        assert_eq!(worker.cooldowns["slow.test"].strikes, 2);
        assert!(worker.is_cooling("slow.test"));
        assert!(!worker.is_cooling("other.test"));

        worker.cooldowns.remove("slow.test");
        worker.leave_alone(ask());
        assert_eq!(worker.cooldowns["slow.test"].strikes, 1);
    }

    fn worker(pool: SqlitePool, checker: Checker) -> Worker {
        Worker {
            pool,
            checker,
            batch: 10,
            cooldowns: HashMap::new(),
        }
    }

    #[tokio::test]
    async fn a_cooling_site_does_not_fill_the_batch_and_starve_other_sites() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "mei").await.unwrap();
        opt_in(&pool, user).await;
        for page in 0..4 {
            add_bookmark(&pool, user, &format!("http://10.0.0.1/{page}"), None).await;
        }
        let elsewhere = add_bookmark(&pool, user, "http://10.0.0.2/", None).await;
        let mut worker = worker(pool.clone(), Checker::public_only().unwrap());
        worker.batch = 2;
        worker.leave_alone(Backoff {
            host: "10.0.0.1".to_owned(),
            retry_after: None,
        });

        let due = worker.due_now().await.unwrap();
        assert_eq!(ids(&due), [elsewhere]);
    }

    #[tokio::test]
    async fn results_are_stored_only_while_the_user_still_wants_the_check() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "mei").await.unwrap();
        assert!(!users::wants_background_check(&pool, user).await.unwrap());
        opt_in(&pool, user).await;
        assert!(users::wants_background_check(&pool, user).await.unwrap());

        users::set_link_check_enabled(&pool, user, false)
            .await
            .unwrap();
        assert!(!users::wants_background_check(&pool, user).await.unwrap());
        opt_in(&pool, user).await;
        sqlx::query("UPDATE users SET disabled_at = 1 WHERE id = ?1")
            .bind(user)
            .execute(&pool)
            .await
            .unwrap();
        assert!(!users::wants_background_check(&pool, user).await.unwrap());
    }

    #[tokio::test]
    async fn a_round_stores_results_and_the_next_round_skips_them() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "mei").await.unwrap();
        opt_in(&pool, user).await;
        // Not public, so no request is made and the verdict is "unknown".
        let id = add_bookmark(&pool, user, "http://10.0.0.1/", None).await;
        let mut worker = worker(pool.clone(), Checker::public_only().unwrap());

        assert_eq!(worker.run_round().await.unwrap(), 1);
        let (status, checked_at): (String, i64) =
            sqlx::query_as("SELECT link_status, link_checked_at FROM bookmarks WHERE id = ?1")
                .bind(id)
                .fetch_one(&pool)
                .await
                .unwrap();
        assert_eq!(status, "unknown");
        assert!(checked_at > 0);
        assert_eq!(worker.run_round().await.unwrap(), 0, "nothing is due now");
    }

    /// A site that answers every request with 429 and asks for a short wait.
    async fn throttling_site() -> (u16, std::sync::Arc<std::sync::atomic::AtomicUsize>) {
        use std::sync::atomic::{AtomicUsize, Ordering};
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let requests = std::sync::Arc::new(AtomicUsize::new(0));
        let counter = requests.clone();
        tokio::spawn(async move {
            loop {
                let Ok((mut socket, _)) = listener.accept().await else {
                    return;
                };
                counter.fetch_add(1, Ordering::SeqCst);
                tokio::spawn(async move {
                    let mut buffer = [0u8; 2048];
                    let _ = socket.read(&mut buffer).await;
                    let response = "HTTP/1.1 429 Too Many Requests\r\nRetry-After: 30\r\n\
                                    Content-Length: 0\r\nConnection: close\r\n\r\n";
                    let _ = socket.write_all(response.as_bytes()).await;
                });
            }
        });
        (port, requests)
    }

    #[tokio::test]
    async fn a_site_that_says_slow_down_is_asked_once_and_then_left_alone() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "mei").await.unwrap();
        opt_in(&pool, user).await;
        let (port, requests) = throttling_site().await;
        for page in ["a", "b", "c"] {
            add_bookmark(
                &pool,
                user,
                &format!("http://127.0.0.1:{port}/{page}"),
                None,
            )
            .await;
        }
        let mut worker = worker(pool.clone(), Checker::allowing_loopback().unwrap());

        assert_eq!(worker.run_round().await.unwrap(), 1);
        assert_eq!(requests.load(std::sync::atomic::Ordering::SeqCst), 1);
        assert_eq!(
            worker.run_round().await.unwrap(),
            0,
            "the site is still cooling down"
        );
        assert_eq!(requests.load(std::sync::atomic::Ordering::SeqCst), 1);
    }
}
