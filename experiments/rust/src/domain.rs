use chrono::{DateTime, Utc};
use std::{fmt, future::Future, pin::Pin, sync::Arc};
use tokio_util::sync::CancellationToken;
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Error {
    Config(&'static str),
    Storage,
    NotFound,
    Exists,
    Denied,
    Busy,
    Removing,
    Closed,
    Invalid,
    Check,
    Authentication,
    Protocol,
    Transport,
    Canceled,
    Deadline,
    Unauthorized,
    RateLimited(u64),
    Telegram,
}
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Config(s) => f.write_str(s),
            _ => write!(f, "operation failed: {self:?}"),
        }
    }
}
impl std::error::Error for Error {}
pub type Result<T> = std::result::Result<T, Error>;
pub type BoxFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;
pub trait Clock: Send + Sync {
    fn now(&self) -> DateTime<Utc>;
}
pub struct SystemClock;
impl Clock for SystemClock {
    fn now(&self) -> DateTime<Utc> {
        Utc::now()
    }
}
pub type SharedClock = Arc<dyn Clock>;
pub trait Portal: Send + Sync {
    fn check<'a>(
        &'a self,
        cancel: &'a CancellationToken,
        owner: i64,
        number: &'a str,
        password: &'a str,
    ) -> BoxFuture<'a, Result<String>>;
    fn forget(&self, owner: i64);
}
#[derive(Clone, Default, Debug)]
pub struct Status {
    pub stage: String,
    pub checked_at: Option<DateTime<Utc>>,
    pub fresh: bool,
    pub in_progress: bool,
    pub next_eligible_at: Option<DateTime<Utc>>,
}
#[derive(Clone)]
pub struct Enrollment {
    pub id: String,
    pub owner: i64,
    pub number: String,
    pub password: String,
    pub stage: String,
    pub checked_at: DateTime<Utc>,
}
pub async fn bounded<T>(
    cancel: &CancellationToken,
    duration: std::time::Duration,
    work: impl Future<Output = Result<T>>,
) -> Result<T> {
    tokio::select! {
    biased;
    _ = cancel.cancelled() => Err(Error::Canceled), result = tokio::time::timeout(duration, work) => result.unwrap_or(Err(Error::Deadline)) }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test(start_paused = true)]
    async fn deadlines_and_cancellation_are_bounded() {
        let cancel = CancellationToken::new();
        let result = bounded(&cancel, std::time::Duration::from_secs(60), async {
            std::future::pending::<Result<()>>().await
        })
        .await;
        assert_eq!(result, Err(Error::Deadline));
        cancel.cancel();
        assert_eq!(
            bounded(&cancel, std::time::Duration::from_secs(60), async {
                Ok(())
            })
            .await,
            Err(Error::Canceled)
        );
    }
}
