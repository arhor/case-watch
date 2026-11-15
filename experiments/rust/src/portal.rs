use crate::domain::*;
use reqwest::{
    Client, Method,
    cookie::{CookieStore, Jar},
    header,
};
use serde_json::{Value, json};
use std::{
    collections::{HashMap, HashSet},
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio_util::sync::CancellationToken;
pub struct Gdansk {
    client: Client,
    base: String,
    jars: Mutex<HashMap<i64, Arc<Jar>>>,
    interval: Duration,
}
impl Gdansk {
    pub fn new(base: &str) -> Result<Self> {
        Ok(Self {
            client: Client::builder()
                .redirect(reqwest::redirect::Policy::none())
                .retry(reqwest::retry::never())
                .build()
                .map_err(|_| Error::Transport)?,
            base: base.trim_end_matches('/').into(),
            jars: Mutex::new(HashMap::new()),
            interval: Duration::from_secs(2),
        })
    }
    async fn run(
        &self,
        cancel: &CancellationToken,
        owner: i64,
        number: &str,
        password: &str,
    ) -> Result<String> {
        let jar = self.jars.lock().unwrap().entry(owner).or_default().clone();
        let mut session = Session {
            adapter: self,
            jar,
            cancel,
            last: None,
            count: 0,
        };
        let b = session.request(Method::GET, "/", None).await?;
        let uidl = bootstrap(&b, "var uidl =")?;
        let config = bootstrap(&b, "var config =")?;
        let ui = integer(&config["v-uiId"])?;
        let mut state = State::default();
        state.apply(&uidl)?;
        if let Ok(stage) = state.stage() {
            return Ok(stage);
        }
        state.login()?;
        if state.token.is_empty() {
            return Err(Error::Protocol);
        }
        for step in 0..4 {
            let (n, p, b) = state.login()?;
            let rpc = match step {
                0 => json!([event(p, "value-changed")]),
                1 => change(n, number, "change"),
                2 => change(p, password, "value-changed"),
                _ => json!([event(b, "click")]),
            };
            let bytes = session
                .request(
                    Method::POST,
                    &format!("/?v-r=uidl&v-uiId={ui}"),
                    Some(json!({"csrfToken":state.token,"syncId":state.sync,"clientId":state.client,"rpc":rpc})),
                )
                .await?;
            let raw = bytes.strip_prefix(b"for(;;);").unwrap_or(&bytes);
            let messages: Vec<Value> = serde_json::from_slice(raw).map_err(|_| Error::Protocol)?;
            if messages.is_empty() {
                return Err(Error::Protocol);
            }
            for message in messages {
                state.apply(&message)?;
            }
        }
        state.stage()
    }
}
impl Portal for Gdansk {
    fn check<'a>(
        &'a self,
        cancel: &'a CancellationToken,
        owner: i64,
        number: &'a str,
        password: &'a str,
    ) -> BoxFuture<'a, Result<String>> {
        Box::pin(async move {
            let r = bounded(
                cancel,
                Duration::from_secs(60),
                self.run(cancel, owner, number, password),
            )
            .await;
            if r.is_err() {
                self.forget(owner)
            }
            r
        })
    }
    fn forget(&self, owner: i64) {
        self.jars.lock().unwrap().remove(&owner);
    }
}
struct Session<'a> {
    adapter: &'a Gdansk,
    jar: Arc<Jar>,
    cancel: &'a CancellationToken,
    last: Option<tokio::time::Instant>,
    count: usize,
}
impl Session<'_> {
    async fn request(
        &mut self,
        method: Method,
        path: &str,
        body: Option<Value>,
    ) -> Result<Vec<u8>> {
        if self.count >= 6 {
            return Err(Error::Protocol);
        }
        if let Some(last) = self.last {
            tokio::select! {
            biased;
            _=self.cancel.cancelled()=>return Err(Error::Canceled),_=tokio::time::sleep_until(last+self.adapter.interval)=>{
            }
            }
        }
        let url = reqwest::Url::parse(&format!("{}{}", self.adapter.base, path))
            .map_err(|_| Error::Protocol)?;
        let mut request = self
            .adapter
            .client
            .request(method, url.clone())
            .header(header::CONTENT_TYPE, "application/json; charset=UTF-8");
        if let Some(cookie) = self.jar.cookies(&url) {
            request = request.header(header::COOKIE, cookie)
        }
        if let Some(body) = body {
            request = request.body(serde_json::to_vec(&body).map_err(|_| Error::Protocol)?)
        }
        self.count += 1;
        self.last = Some(tokio::time::Instant::now());
        bounded(self.cancel, Duration::from_secs(10), async {
            let mut response = request.send().await.map_err(|_| Error::Transport)?;
            if [401, 403].contains(&response.status().as_u16()) {
                return Err(Error::Authentication);
            }
            if response.status() != 200 {
                return Err(Error::Transport);
            }
            self.jar.set_cookies(
                &mut response.headers().get_all(header::SET_COOKIE).iter(),
                &url,
            );
            let mut bytes = Vec::new();
            while let Some(chunk) = response.chunk().await.map_err(|_| Error::Transport)? {
                if bytes.len() + chunk.len() > 4 << 20 {
                    return Err(Error::Protocol);
                }
                bytes.extend_from_slice(&chunk);
            }
            Ok(bytes)
        })
        .await
    }
}
fn bootstrap(bytes: &[u8], marker: &str) -> Result<Value> {
    let at = bytes
        .windows(marker.len())
        .position(|w| w == marker.as_bytes())
        .ok_or(Error::Protocol)?
        + marker.len();
    let mut stream = serde_json::Deserializer::from_slice(&bytes[at..]).into_iter::<Value>();
    stream
        .next()
        .ok_or(Error::Protocol)?
        .map_err(|_| Error::Protocol)
}
fn integer(v: &Value) -> Result<u64> {
    let n = v.as_f64().ok_or(Error::Protocol)?;
    if n < 0.0 || n > 9007199254740991.0 || n.fract() != 0.0 {
        return Err(Error::Protocol);
    }
    Ok(n as u64)
}
fn event(node: u64, name: &str) -> Value {
    let data = if name == "click" {
        json!({"event.shiftKey":false,"event.metaKey":false,"event.ctrlKey":false,"event.altKey":false,"event.detail":0,"event.clientX":0,"event.clientY":0,"event.button":0,"event.screenX":0,"event.screenY":0})
    } else {
        json!({})
    };
    json!({"type":"event","node":node,"event":name,"data":data})
}
fn change(node: u64, value: &str, event_name: &str) -> Value {
    json!([{"type":"mSync","node":node,"feature":1,"property":"value","value":value},event(node,event_name)])
}
#[derive(Default)]
struct State {
    props: HashMap<u64, HashMap<String, Value>>,
    children: HashMap<u64, Vec<u64>>,
    sync: u64,
    client: u64,
    token: String,
}
impl State {
    fn apply(&mut self, v: &Value) -> Result<()> {
        self.sync = integer(&v["syncId"])?;
        self.client = integer(&v["clientId"])?;
        if let Some(t) = v["Vaadin-Security-Key"].as_str() {
            self.token = t.into()
        }
        let Some(changes) = v.get("changes") else {
            return Ok(());
        };
        for c in changes.as_array().ok_or(Error::Protocol)? {
            let n = integer(&c["node"])?;
            self.props.entry(n).or_default();
            let feat = c["feat"].as_u64().unwrap_or(0);
            match c["type"].as_str().ok_or(Error::Protocol)? {
                "attach" => {}
                "put" => {
                    let key = c["key"].as_str().ok_or(Error::Protocol)?;
                    if [0, 1, 7].contains(&feat) {
                        self.props
                            .get_mut(&n)
                            .unwrap()
                            .insert(key.into(), c["value"].clone());
                    }
                }
                "remove" => {
                    if [0, 1, 7].contains(&feat) {
                        self.props
                            .get_mut(&n)
                            .unwrap()
                            .remove(c["key"].as_str().unwrap_or(""));
                    }
                }
                "detach" => {
                    self.props.remove(&n);
                    self.children.remove(&n);
                }
                "clear" | "empty" => {
                    if feat == 2 {
                        self.children.remove(&n);
                    }
                }
                "splice" => {
                    if feat != 2 {
                        continue;
                    }
                    let index = integer(&c["index"])? as usize;
                    let remove = integer(c.get("remove").unwrap_or(&json!(0)))? as usize;
                    let children = self.children.entry(n).or_default();
                    if index > children.len() || remove > children.len() - index {
                        return Err(Error::Protocol);
                    }
                    let mut added = Vec::new();
                    if let Some(nodes) = c.get("addNodes") {
                        for n in nodes.as_array().ok_or(Error::Protocol)? {
                            added.push(integer(n)?);
                        }
                    }
                    children.splice(index..index + remove, added);
                }
                _ => return Err(Error::Protocol),
            }
        }
        Ok(())
    }
    fn prop(&self, n: u64, key: &str) -> &Value {
        self.props
            .get(&n)
            .and_then(|p| p.get(key))
            .unwrap_or(&Value::Null)
    }
    fn find(&self, tag: &str, label: &str) -> Result<u64> {
        let nodes: Vec<_> =
            self.props
                .keys()
                .copied()
                .filter(|&n| {
                    self.prop(n, "tag") == tag
                        && (label.is_empty() || self.prop(n, "label") == label)
                        && (tag != "vaadin-button"
                            || self.children.get(&n).is_some_and(|cs| {
                                cs.iter().any(|&c| self.prop(c, "text") == "Log in")
                            }))
                })
                .collect();
        if nodes.len() != 1 {
            return Err(Error::Protocol);
        }
        Ok(nodes[0])
    }
    fn login(&self) -> Result<(u64, u64, u64)> {
        Ok((
            self.find("vaadin-text-field", "Number of application")?,
            self.find("vaadin-password-field", "Password")?,
            self.find("vaadin-button", "")?,
        ))
    }
    fn walk(&self, n: u64, seen: &mut HashSet<u64>, nodes: &mut Vec<u64>) {
        if !seen.insert(n) {
            return;
        }
        nodes.push(n);
        if let Some(cs) = self.children.get(&n) {
            for &c in cs {
                self.walk(c, seen, nodes)
            }
        }
    }
    fn stage(&self) -> Result<String> {
        for &n in self.children.keys() {
            let mut nodes = Vec::new();
            self.walk(n, &mut HashSet::new(), &mut nodes);
            if !nodes.iter().any(|&n| {
                self.prop(n, "text") == "Stage of your case"
                    || self.prop(n, "nodeValue") == "Stage of your case"
            }) {
                continue;
            }
            let values: Vec<_> = nodes
                .iter()
                .filter(|&&n| self.prop(n, "tag") == "vaadin-text-field")
                .filter_map(|&n| self.prop(n, "value").as_str())
                .filter(|s| !s.trim().is_empty())
                .collect();
            if values.len() == 1 {
                return Ok(values[0].into());
            }
        }
        if self.find("vaadin-password-field", "Password").is_ok() {
            Err(Error::Authentication)
        } else {
            Err(Error::Protocol)
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn rpc_state_rejects_invalid_changes_and_ambiguous_login() {
        let mut state = State::default();
        let bootstrap = bootstrap(
            include_bytes!("../../../internal/clients/testdata/bootstrap.html"),
            "var uidl =",
        )
        .unwrap();
        state.apply(&bootstrap).unwrap();
        assert!(state.login().is_ok());
        for change in [
            json!({"node":-1,"type":"attach"}),
            json!({"node":1,"type":"unknown"}),
            json!({"node":1,"type":"splice","feat":2,"index":999999}),
            json!({"node":1,"type":"put","key":null}),
        ] {
            assert_eq!(
                state.apply(&json!({"syncId":1,"clientId":1,"changes":[change]})),
                Err(Error::Protocol)
            );
        }
        assert_eq!(integer(&json!(1.5)), Err(Error::Protocol));
        assert_eq!(integer(&json!(9007199254740992_u64)), Err(Error::Protocol));
    }
    #[tokio::test]
    async fn spacing_wait_is_cancellable() {
        let adapter = Gdansk::new("http://127.0.0.1:1").unwrap();
        let cancel = CancellationToken::new();
        let mut session = Session {
            adapter: &adapter,
            jar: Arc::new(Jar::default()),
            cancel: &cancel,
            last: Some(tokio::time::Instant::now()),
            count: 1,
        };
        cancel.cancel();
        assert_eq!(
            session.request(Method::GET, "/", None).await,
            Err(Error::Canceled)
        );
        assert_eq!(session.count, 1);
    }
}
