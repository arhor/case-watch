mod support;
use case_watch_rust::{domain::*, portal::Gdansk};
use support::Server;
use tokio_util::sync::CancellationToken;
const FIXTURES: [&[u8]; 5] = [
    include_bytes!("../../../internal/clients/testdata/bootstrap.html"),
    include_bytes!("../../../internal/clients/testdata/rpc0.json"),
    include_bytes!("../../../internal/clients/testdata/rpc1.json"),
    include_bytes!("../../../internal/clients/testdata/rpc2.json"),
    include_bytes!("../../../internal/clients/testdata/rpc3.json"),
];
#[tokio::test]
async fn live_adapter_uses_fixture_protocol_and_reuses_isolated_cookies() {
    let server = Server::start(|r, n| {
        let step = n % 5;
        if step > 0 {
            assert!(r.headers.to_lowercase().contains("cookie: session=fresh"));
            let v: serde_json::Value = serde_json::from_slice(&r.body).unwrap();
            assert!(v["rpc"].is_array());
            assert!(r.path.contains("v-r=uidl"));
        } else if n == 0 || n == 10 {
            assert!(!r.headers.to_lowercase().contains("cookie:"));
        }
        (
            200,
            "Set-Cookie: session=fresh; Path=/\r\n".into(),
            FIXTURES[step].to_vec(),
        )
    })
    .await;
    let client = Gdansk::new(&server.url).unwrap();
    for owner in [1, 1, 2] {
        assert_eq!(
            client
                .check(
                    &CancellationToken::new(),
                    owner,
                    "synthetic-case",
                    "synthetic-password"
                )
                .await
                .unwrap(),
            "Synthetic stage"
        );
    }
    assert_eq!(server.requests.lock().unwrap().len(), 15);
}
#[tokio::test]
async fn failures_are_sanitized_and_never_retried() {
    for (code, body, expected) in [
        (403, b"private-secret".to_vec(), Error::Authentication),
        (302, vec![], Error::Transport),
        (200, b"unexpected page".to_vec(), Error::Protocol),
        (200, vec![b'x'; (4 << 20) + 1], Error::Protocol),
    ] {
        let server = Server::start(move |_, _| (code, String::new(), body.clone())).await;
        let client = Gdansk::new(&server.url).unwrap();
        assert_eq!(
            client
                .check(&CancellationToken::new(), 1, "case", "secret")
                .await,
            Err(expected)
        );
        assert_eq!(server.requests.lock().unwrap().len(), 1);
        assert!(!expected.to_string().contains("secret"));
    }
}
#[tokio::test]
async fn canceled_check_makes_no_requests() {
    let server = Server::start(|_, _| panic!("request after cancellation")).await;
    let t = CancellationToken::new();
    t.cancel();
    assert_eq!(
        Gdansk::new(&server.url)
            .unwrap()
            .check(&t, 1, "a", "b")
            .await,
        Err(Error::Canceled)
    );
    assert!(server.requests.lock().unwrap().is_empty());
}

fn authenticated_page() -> Vec<u8> {
    let source = std::str::from_utf8(FIXTURES[0]).unwrap();
    let at = source.find("var uidl =").unwrap() + "var uidl =".len();
    let mut uidl = serde_json::Deserializer::from_str(&source[at..])
        .into_iter::<serde_json::Value>()
        .next()
        .unwrap()
        .unwrap();
    for bytes in &FIXTURES[1..] {
        let raw = bytes.strip_prefix(b"for(;;);").unwrap_or(bytes);
        let messages: Vec<serde_json::Value> = serde_json::from_slice(raw).unwrap();
        for message in messages {
            if let Some(changes) = message["changes"].as_array() {
                uidl["changes"]
                    .as_array_mut()
                    .unwrap()
                    .extend(changes.clone());
            }
            uidl["syncId"] = message["syncId"].clone();
            uidl["clientId"] = message["clientId"].clone();
        }
    }
    format!("var uidl = {uidl}; var config = {{\"v-uiId\":0}};").into_bytes()
}
#[tokio::test]
async fn stale_pooled_connection_does_not_replay_get() {
    use tokio::{
        io::{AsyncReadExt, AsyncWriteExt},
        net::TcpListener,
    };
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let endpoint = format!("http://{}", listener.local_addr().unwrap());
    let body = authenticated_page();
    let task = tokio::spawn(async move {
        let (mut stream, _) = listener.accept().await.unwrap();
        let mut bytes = Vec::new();
        loop {
            let mut b = [0];
            stream.read_exact(&mut b).await.unwrap();
            bytes.push(b[0]);
            if bytes.ends_with(b"\r\n\r\n") {
                break;
            }
        }
        stream
            .write_all(
                format!("HTTP/1.1 200 OK\r\nContent-Length: {}\r\n\r\n", body.len()).as_bytes(),
            )
            .await
            .unwrap();
        stream.write_all(&body).await.unwrap();
        bytes.clear();
        loop {
            let mut b = [0];
            stream.read_exact(&mut b).await.unwrap();
            bytes.push(b[0]);
            if bytes.ends_with(b"\r\n\r\n") {
                break;
            }
        }
        drop(stream);
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(250), listener.accept())
                .await
                .is_err(),
            "request was replayed"
        );
    });
    let client = Gdansk::new(&endpoint).unwrap();
    let t = CancellationToken::new();
    assert_eq!(
        client.check(&t, 1, "a", "b").await.unwrap(),
        "Synthetic stage"
    );
    assert_eq!(client.check(&t, 1, "a", "b").await, Err(Error::Transport));
    task.await.unwrap();
}
