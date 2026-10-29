use std::{
    net::{TcpListener, TcpStream},
    sync::Mutex,
    thread,
    time::{Duration, Instant},
};

use tauri::{Manager, RunEvent, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_shell::{process::CommandChild, ShellExt};

// Fixed so the page origin, and the browser storage tied to it, is the same on every launch.
const PREFERRED_PORT: u16 = 47821;

struct Sidecar(Mutex<Option<CommandChild>>);

fn pick_port() -> u16 {
    if TcpListener::bind(("127.0.0.1", PREFERRED_PORT)).is_ok() {
        return PREFERRED_PORT;
    }
    TcpListener::bind(("127.0.0.1", 0))
        .and_then(|listener| listener.local_addr())
        .map(|addr| addr.port())
        .unwrap_or(PREFERRED_PORT)
}

fn wait_for_server(addr: &str) -> Result<(), String> {
    let socket = addr.parse().map_err(|e| format!("bad address {addr}: {e}"))?;
    let deadline = Instant::now() + Duration::from_secs(20);
    while Instant::now() < deadline {
        if TcpStream::connect_timeout(&socket, Duration::from_millis(200)).is_ok() {
            return Ok(());
        }
        thread::sleep(Duration::from_millis(100));
    }
    Err(format!("the Utane server did not start on {addr}"))
}

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_single_instance::init(|app, _, _| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.unminimize();
                let _ = window.set_focus();
            }
        }))
        .setup(|app| {
            let addr = format!("127.0.0.1:{}", pick_port());

            let (mut events, child) = app
                .shell()
                .sidecar("utane")?
                .env("UTANE_ADDR", &addr)
                .args(["serve"])
                .spawn()?;
            // Keep draining the sidecar's output so its pipes never fill up.
            tauri::async_runtime::spawn(async move { while events.recv().await.is_some() {} });
            app.manage(Sidecar(Mutex::new(Some(child))));

            wait_for_server(&addr)?;
            WebviewWindowBuilder::new(
                app,
                "main",
                WebviewUrl::External(format!("http://{addr}").parse()?),
            )
            .title("Utane")
            .inner_size(1200.0, 800.0)
            .build()?;

            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building Utane")
        .run(|app, event| {
            if let RunEvent::Exit = event {
                // Without this the Go server would outlive the window and keep the database open.
                if let Some(child) = app.state::<Sidecar>().0.lock().unwrap().take() {
                    let _ = child.kill();
                }
            }
        });
}
