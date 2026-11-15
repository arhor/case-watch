use crate::domain::{Error, Result};
pub struct Config {
    pub token: String,
    pub db_path: String,
}
impl Config {
    pub fn load(get: impl Fn(&str) -> Option<String>) -> Result<Self> {
        let token = get("CASE_WATCH_TELEGRAM_BOT_TOKEN").unwrap_or_default();
        let valid = token.split_once(':').is_some_and(|(id, secret)| {
            !id.is_empty()
                && id.as_bytes()[0] != b'0'
                && id.bytes().all(|c| c.is_ascii_digit())
                && secret.len() == 35
                && secret
                    .bytes()
                    .all(|c| c.is_ascii_alphanumeric() || c == b'_' || c == b'-')
        });
        if !valid {
            return Err(Error::Config(
                "CASE_WATCH_TELEGRAM_BOT_TOKEN: invalid or missing token syntax",
            ));
        }
        let db_path = get("CASE_WATCH_DB_PATH").unwrap_or_default();
        if db_path.trim().is_empty() || db_path.contains(['\0', '\r', '\n']) {
            return Err(Error::Config("CASE_WATCH_DB_PATH: invalid or missing path"));
        }
        Ok(Self { token, db_path })
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn configuration_is_validated_without_echoing_secrets() {
        let token = format!("123:{}", "a".repeat(35));
        assert!(
            Config::load(|k| Some(if k.ends_with("TOKEN") {
                token.clone()
            } else {
                "case.db".into()
            }))
            .is_ok()
        );
        for value in ["", "0:secret", " secret "] {
            assert!(Config::load(|_| Some(value.into())).is_err());
        }
        assert!(
            !Config::load(|_| Some("private-secret".into()))
                .err()
                .unwrap()
                .to_string()
                .contains("private-secret")
        );
    }
}
