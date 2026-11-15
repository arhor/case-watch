use std::sync::{Arc, Mutex};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
};
#[derive(Clone, Debug)]
pub struct Request {
    pub path: String,
    pub headers: String,
    pub body: Vec<u8>,
}
pub struct Server {
    pub url: String,
    pub requests: Arc<Mutex<Vec<Request>>>,
    task: tokio::task::JoinHandle<()>,
}
impl Drop for Server {
    fn drop(&mut self) {
        self.task.abort();
    }
}
impl Server {
    pub async fn start(
        handler: impl Fn(&Request, usize) -> (u16, String, Vec<u8>) + Send + Sync + 'static,
    ) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        let requests = Arc::new(Mutex::new(Vec::new()));
        let log = requests.clone();
        let handler = Arc::new(handler);
        let task = tokio::spawn(async move {
            loop {
                let (mut stream, _) = listener.accept().await.unwrap();
                let log = log.clone();
                let handler = handler.clone();
                tokio::spawn(async move {
                    loop {
                        let mut bytes = Vec::new();
                        let mut one = [0];
                        while !bytes.ends_with(b"\r\n\r\n") {
                            if stream.read(&mut one).await.unwrap_or(0) == 0 {
                                return;
                            }
                            bytes.push(one[0]);
                        }
                        let headers = String::from_utf8(bytes).unwrap();
                        let len = headers
                            .lines()
                            .find_map(|l| {
                                l.to_lowercase()
                                    .strip_prefix("content-length:")
                                    .map(|s| s.trim().parse::<usize>().unwrap())
                            })
                            .unwrap_or(0);
                        let mut body = vec![0; len];
                        if stream.read_exact(&mut body).await.is_err() {
                            return;
                        }
                        let r = Request {
                            path: headers.split_whitespace().nth(1).unwrap().into(),
                            headers,
                            body,
                        };
                        let index = {
                            let mut log = log.lock().unwrap();
                            let n = log.len();
                            log.push(r.clone());
                            n
                        };
                        let (status, extra, body) = handler(&r, index);
                        let header = format!(
                            "HTTP/1.1 {status} Response\r\nContent-Length: {}\r\n{extra}\r\n",
                            body.len()
                        );
                        if stream.write_all(header.as_bytes()).await.is_err()
                            || stream.write_all(&body).await.is_err()
                        {
                            return;
                        }
                    }
                });
            }
        });
        Self {
            url,
            requests,
            task,
        }
    }
}
