mod support;
use case_watch_rust::{
    domain::*,
    service::Cases,
    storage::Store,
    telegram::{Bot, format_status, private_owner},
};
use serde_json::{Value, json};
use std::sync::{Arc, Mutex};
use support::Server;
use tokio_util::sync::CancellationToken;
struct PortalOk;
impl Portal for PortalOk {
    fn check<'a>(
        &'a self,
        _: &'a CancellationToken,
        _: i64,
        _: &'a str,
        _: &'a str,
    ) -> BoxFuture<'a, Result<String>> {
        Box::pin(async { Ok("Synthetic stage".into()) })
    }
    fn forget(&self, _: i64) {}
}
#[tokio::test]
async fn conversation_enrolls_and_only_confirmation_removes() {
    let replies = Arc::new(Mutex::new(Vec::new()));
    let log = replies.clone();
    let server = Server::start(move |r, _| {
        assert!(r.headers.contains("POST"));
        let v: Value = serde_json::from_slice(&r.body).unwrap();
        let result = if r.path.ends_with("getMe") {
            json!({"id":1,"is_bot":true,"first_name":"test"})
        } else if r.path.ends_with("getMyCommands") {
            json!([])
        } else if r.path.ends_with("sendMessage") {
            log.lock()
                .unwrap()
                .push(v["text"].as_str().unwrap().to_string());
            json!({"message_id":1})
        } else {
            json!(true)
        };
        (
            200,
            "Content-Type: application/json\r\n".into(),
            serde_json::to_vec(&json!({"ok":true,"result":result})).unwrap(),
        )
    })
    .await;
    let cases = Cases::new(
        Store::memory().unwrap(),
        Arc::new(PortalOk),
        Arc::new(SystemClock),
        CancellationToken::new(),
    );
    let bot = Bot::new(
        "synthetic-token",
        &server.url,
        cases.clone(),
        CancellationToken::new(),
    )
    .await
    .unwrap();
    let update = |text: &str| json!({"message":{"message_id":10,"from":{"id":7,"is_bot":false},"chat":{"id":7,"type":"private"},"text":text}});
    for text in ["/start", "synthetic-case", "synthetic-password"] {
        bot.handle(&update(text)).await;
    }
    for _ in 0..100 {
        if cases.exists(7).await.unwrap() {
            break;
        }
        tokio::task::yield_now().await;
    }
    assert!(cases.exists(7).await.unwrap());
    bot.handle(&update("/status")).await;
    bot.handle(&update("/remove")).await;
    bot.handle(&update("perhaps")).await;
    assert!(cases.exists(7).await.unwrap());
    bot.handle(&update("Нет")).await;
    assert!(cases.exists(7).await.unwrap());
    bot.handle(&update("/remove")).await;
    bot.handle(&update(" Да ")).await;
    assert!(!cases.exists(7).await.unwrap());
    bot.close().await;
    let requests = server.requests.lock().unwrap();
    assert_eq!(
        requests
            .iter()
            .filter(|r| r.path.ends_with("deleteMessage"))
            .count(),
        2
    );
    let output = replies.lock().unwrap();
    assert!(output.iter().any(|s| s.contains("Synthetic stage")));
    assert!(
        output
            .iter()
            .any(|s| s == "Пожалуйста, ответьте «Да» или «Нет».")
    );
    assert!(!output.iter().any(|s| s.contains("synthetic-password")));
}
#[test]
fn rejects_untrusted_transport_metadata() {
    for m in [
        json!({}),
        json!({"from":{"id":7},"chat":{"id":8,"type":"private"}}),
        json!({"from":{"id":7,"is_bot":true},"chat":{"id":7,"type":"private"}}),
        json!({"from":{"id":7},"chat":{"id":7,"type":"group"}}),
    ] {
        assert_eq!(private_owner(&m), None)
    }
}
#[test]
fn unavailable_and_failed_cache_are_distinct() {
    let out = case_watch_rust::service::Outcome {
        status: Status::default(),
        error: Some(Error::Check),
    };
    let s = format_status(&out);
    assert!(s.contains("Не удалось обновить"));
    assert!(s.contains("Пока нет данных"));
}

struct TestClock(Mutex<chrono::DateTime<chrono::Utc>>);
impl Clock for TestClock {
    fn now(&self) -> chrono::DateTime<chrono::Utc> {
        *self.0.lock().unwrap()
    }
}
#[tokio::test]
async fn expiration_validation_and_command_arguments() {
    let server = Server::start(|r, _| {
        let result = if r.path.ends_with("getMe") {
            json!({"id":1})
        } else if r.path.ends_with("getMyCommands") {
            json!([])
        } else {
            json!(true)
        };
        (
            200,
            String::new(),
            serde_json::to_vec(&json!({"ok":true,"result":result})).unwrap(),
        )
    })
    .await;
    let clock = Arc::new(TestClock(Mutex::new(chrono::Utc::now())));
    let cases = Cases::new(
        Store::memory().unwrap(),
        Arc::new(PortalOk),
        clock.clone(),
        CancellationToken::new(),
    );
    let bot = Bot::new(
        "synthetic",
        &server.url,
        cases.clone(),
        CancellationToken::new(),
    )
    .await
    .unwrap();
    let u = |text: &str| json!({"message":{"message_id":1,"from":{"id":1},"chat":{"id":1,"type":"private"},"text":text}});
    bot.handle(&u("/start")).await;
    bot.handle(&u("case\ninvalid")).await;
    bot.handle(&u("case")).await;
    *clock.0.lock().unwrap() += chrono::Duration::minutes(10);
    bot.handle(&u("password")).await;
    assert!(!cases.exists(1).await.unwrap());
    bot.handle(&u("/start secret")).await;
    bot.handle(&u("/help")).await;
    bot.close().await;
    let requests = server.requests.lock().unwrap();
    let replies: Vec<String> = requests
        .iter()
        .filter(|r| r.path.ends_with("sendMessage"))
        .map(|r| {
            serde_json::from_slice::<Value>(&r.body).unwrap()["text"]
                .as_str()
                .unwrap()
                .to_string()
        })
        .collect();
    assert!(
        replies
            .iter()
            .any(|s| s.contains("Не удалось принять номер"))
    );
    assert!(replies.iter().any(|s| s.contains("Время ожидания истекло")));
    assert!(
        replies
            .iter()
            .any(|s| s.contains("без дополнительных параметров"))
    );
    assert!(replies.iter().any(|s| s.contains("/help —")));
}
#[tokio::test]
async fn unauthorized_startup_is_sanitized() {
    let server = Server::start(|_, _| (401, String::new(), b"secret-token".to_vec())).await;
    let cases = Cases::new(
        Store::memory().unwrap(),
        Arc::new(PortalOk),
        Arc::new(SystemClock),
        CancellationToken::new(),
    );
    let token = CancellationToken::new();
    let result = Bot::new("secret-token", &server.url, cases, token.clone()).await;
    assert!(matches!(result, Err(Error::Unauthorized)));
    assert!(token.is_cancelled());
    assert_eq!(server.requests.lock().unwrap().len(), 1);
}

#[tokio::test]
async fn polling_dispatches_help_and_cancellation_drains() {
    use std::sync::atomic::{AtomicBool, Ordering};
    let sent = Arc::new(AtomicBool::new(false));
    let signal = sent.clone();
    let server=Server::start(move|r,n|{let result=if r.path.ends_with("getMe"){json!({"id":1})}else if r.path.ends_with("getMyCommands"){json!([])}else if r.path.ends_with("getUpdates"){let v:Value=serde_json::from_slice(&r.body).unwrap();assert_eq!(v["timeout"],59);if v["offset"]==1{json!([{"update_id":9,"message":{"message_id":1,"from":{"id":1},"chat":{"id":1,"type":"private"},"text":"/help"}}])}else{assert_eq!(v["offset"],10);json!([])}}else{if r.path.ends_with("sendMessage"){signal.store(true,Ordering::SeqCst);}let _=n;json!(true)};(200,String::new(),serde_json::to_vec(&json!({"ok":true,"result":result})).unwrap())}).await;
    let cases = Cases::new(
        Store::memory().unwrap(),
        Arc::new(PortalOk),
        Arc::new(SystemClock),
        CancellationToken::new(),
    );
    let cancel = CancellationToken::new();
    let bot = Bot::new("synthetic", &server.url, cases, cancel.clone())
        .await
        .unwrap();
    let b = bot.clone();
    let task = tokio::spawn(async move { b.run().await });
    tokio::time::timeout(std::time::Duration::from_secs(5), async {
        while !sent.load(Ordering::SeqCst) {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    assert_eq!(bot.run().await, Err(Error::Busy));
    cancel.cancel();
    task.await.unwrap().unwrap();
}
