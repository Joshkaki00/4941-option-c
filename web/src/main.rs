mod model;

use std::cell::Cell;
use std::rc::Rc;

use futures_util::StreamExt;
use gloo_net::http::{Request, Response};
use model::{
    Answer, ConversationCreated, ErrorEvent, FeedbackBody, NewQuestion, SseEvent, TokenEvent,
    bearer, is_valid_question, outcome_label, parse_sse, take_utf8,
};
use yew::platform::spawn_local;
use yew::prelude::*;

/// Same-origin API prefix. `trunk serve` proxies /v1 to the Go server (see Trunk.toml).
const API: &str = "/v1";

fn check(resp: &Response) -> Result<(), String> {
    match resp.status() {
        200..=299 => Ok(()),
        401 => Err("That access token was rejected.".into()),
        403 => Err("This token is not allowed to ask questions.".into()),
        404 => Err("That conversation was not found. Ask again to start a new one.".into()),
        code => Err(format!("The server returned HTTP {code}.")),
    }
}

fn net_err(e: impl std::fmt::Display) -> String {
    format!("Network or response error: {e}")
}

/// Creates a conversation when none exists yet and returns its id.
async fn ensure_conversation(token: &str, conv: Option<String>) -> Result<String, String> {
    if let Some(id) = conv {
        return Ok(id);
    }
    let resp = Request::post(&format!("{API}/conversations"))
        .header("Authorization", &bearer(token))
        .send()
        .await
        .map_err(net_err)?;
    check(&resp)?;
    Ok(resp
        .json::<ConversationCreated>()
        .await
        .map_err(net_err)?
        .id)
}

/// Asks one question and streams the SSE response, calling `on_event` for each event as it
/// arrives. Returns the conversation id so the caller can reuse it.
async fn ask_streaming(
    token: &str,
    conv: Option<String>,
    question: &str,
    mut on_event: impl FnMut(SseEvent),
) -> Result<String, String> {
    let conv = ensure_conversation(token, conv).await?;
    let body = NewQuestion {
        content: question,
        stream: true,
    };
    let resp = Request::post(&format!("{API}/conversations/{conv}/messages"))
        .header("Authorization", &bearer(token))
        .json(&body)
        .map_err(net_err)?
        .send()
        .await
        .map_err(net_err)?;
    check(&resp)?;
    let raw = resp
        .body()
        .ok_or_else(|| "The server sent no answer.".to_string())?;
    let mut chunks = wasm_streams::ReadableStream::from_raw(raw).into_stream();

    let mut pending: Vec<u8> = Vec::new();
    let mut buffer = String::new();
    while let Some(chunk) = chunks.next().await {
        let chunk = chunk.map_err(|e| format!("Stream interrupted: {e:?}"))?;
        pending.extend(js_sys::Uint8Array::new(&chunk).to_vec());
        buffer.push_str(&take_utf8(&mut pending));
        let (events, rest) = parse_sse(&buffer);
        buffer = rest;
        for event in events {
            on_event(event);
        }
    }
    Ok(conv)
}

async fn send_feedback(token: &str, message_id: &str, rating: i8) -> Result<(), String> {
    let body = FeedbackBody {
        rating,
        comment: String::new(),
    };
    let resp = Request::post(&format!("{API}/messages/{message_id}/feedback"))
        .header("Authorization", &bearer(token))
        .json(&body)
        .map_err(net_err)?
        .send()
        .await
        .map_err(net_err)?;
    check(&resp)
}

/// Date part of an RFC 3339 timestamp, for display.
fn day(ts: &str) -> &str {
    ts.split('T').next().unwrap_or(ts)
}

#[component]
fn App() -> Html {
    let token = use_state(String::new);
    let question = use_state(String::new);
    let conv = use_state(|| None::<String>);
    let busy = use_state(|| false);
    let draft = use_state(String::new);
    let result = use_state(|| None::<Result<Answer, String>>);
    let rated = use_state(|| None::<i8>);
    let notice = use_state(String::new);

    let on_token = {
        let (token, conv) = (token.clone(), conv.clone());
        Callback::from(move |e: InputEvent| {
            if let Some(input) = e.target_dyn_into::<web_sys::HtmlInputElement>() {
                token.set(input.value());
                // Conversations belong to one user; switching tokens starts fresh.
                conv.set(None);
            }
        })
    };

    let on_question = {
        let question = question.clone();
        Callback::from(move |e: InputEvent| {
            if let Some(area) = e.target_dyn_into::<web_sys::HtmlTextAreaElement>() {
                question.set(area.value());
            }
        })
    };

    let on_ask = {
        let (token, question, conv, busy, draft, result, rated, notice) = (
            token.clone(),
            question.clone(),
            conv.clone(),
            busy.clone(),
            draft.clone(),
            result.clone(),
            rated.clone(),
            notice.clone(),
        );
        Callback::from(move |_: MouseEvent| {
            if *busy {
                return;
            }
            let q = question.trim().to_string();
            if !is_valid_question(&q) {
                result.set(Some(Err(
                    "Enter a question of up to 1000 characters.".into()
                )));
                return;
            }
            busy.set(true);
            draft.set(String::new());
            rated.set(None);
            notice.set(String::new());
            result.set(None);
            let tok = (*token).clone();
            let existing = (*conv).clone();
            let (conv, busy, draft, result) =
                (conv.clone(), busy.clone(), draft.clone(), result.clone());
            spawn_local(async move {
                let got_done = Rc::new(Cell::new(false));
                let done_flag = got_done.clone();
                let outcome =
                    ask_streaming(&tok, existing, &q, |event| match event.event.as_str() {
                        "token" => {
                            if let Ok(t) = serde_json::from_str::<TokenEvent>(&event.data) {
                                draft.set(format!("{}{}", *draft, t.text));
                            }
                        }
                        "done" => {
                            done_flag.set(true);
                            match serde_json::from_str::<Answer>(&event.data) {
                                Ok(answer) => result.set(Some(Ok(answer))),
                                Err(e) => result.set(Some(Err(net_err(e)))),
                            }
                        }
                        "error" => {
                            if let Ok(e) = serde_json::from_str::<ErrorEvent>(&event.data) {
                                done_flag.set(true);
                                result.set(Some(Err(e.error)));
                            }
                        }
                        _ => {}
                    })
                    .await;

                match outcome {
                    Ok(id) => {
                        conv.set(Some(id));
                        if !got_done.get() {
                            result.set(Some(Err(
                                "The answer stopped early. Please try again.".into()
                            )));
                        }
                    }
                    Err(e) => {
                        // Drop the conversation so the next attempt starts a fresh one.
                        conv.set(None);
                        result.set(Some(Err(e)));
                    }
                }
                busy.set(false);
            });
        })
    };

    let on_rate = {
        let (token, rated, notice) = (token.clone(), rated.clone(), notice.clone());
        Callback::from(move |(message_id, rating): (String, i8)| {
            let tok = (*token).clone();
            let (rated, notice) = (rated.clone(), notice.clone());
            spawn_local(async move {
                match send_feedback(&tok, &message_id, rating).await {
                    Ok(()) => rated.set(Some(rating)),
                    Err(e) => notice.set(format!("Feedback not saved. {e}")),
                }
            });
        })
    };

    let busy_now = *busy;
    html! {
        <main>
            <h1>{ "Ask the Handbook" }</h1>
            <p class="hint">{ "Answers come only from the company handbook and cite their sources." }</p>
            <label>
                { "Access token" }
                <input type="password" autocomplete="off" value={(*token).clone()} oninput={on_token} />
            </label>
            <label>
                { "Your question" }
                <textarea rows="3" value={(*question).clone()} oninput={on_question}
                    placeholder="e.g. How many PTO days do I get?" />
            </label>
            <button class="ask" onclick={on_ask} disabled={busy_now}>
                { if busy_now { "Asking…" } else { "Ask" } }
            </button>
            <p class="notice" role="status">{ (*notice).clone() }</p>
            { if busy_now && (*result).is_none() && !draft.is_empty() {
                html! { <section class="answer"><p class="text">{ (*draft).clone() }</p></section> }
            } else {
                html! {}
            } }
            { view_result(&result, *rated, &on_rate) }
        </main>
    }
}

fn view_result(
    result: &Option<Result<Answer, String>>,
    rated: Option<i8>,
    on_rate: &Callback<(String, i8)>,
) -> Html {
    match result {
        None => html! {},
        Some(Err(e)) => html! { <p class="error" role="alert">{ e.clone() }</p> },
        Some(Ok(answer)) => {
            let message_id = answer.message_id.clone();
            let up = {
                let (cb, id) = (on_rate.clone(), message_id.clone());
                Callback::from(move |_: MouseEvent| cb.emit((id.clone(), 1)))
            };
            let down = {
                let (cb, id) = (on_rate.clone(), message_id.clone());
                Callback::from(move |_: MouseEvent| cb.emit((id.clone(), -1)))
            };
            html! {
                <section class="answer">
                    <p class="outcome">{ outcome_label(&answer.outcome) }</p>
                    <p class="text">{ answer.answer.clone() }</p>
                    { if !answer.handoff.is_empty() && answer.outcome != "answered" {
                        html! { <p class="handoff">{ answer.handoff.clone() }</p> }
                    } else {
                        html! {}
                    } }
                    { if answer.citations.is_empty() {
                        html! {}
                    } else {
                        html! {
                            <>
                                <h2>{ "Sources" }</h2>
                                <ol class="sources">
                                    { for answer.citations.iter().map(|c| html! {
                                        <li>
                                            <span class="doc">{ c.document_title.clone() }</span>
                                            { if c.section.is_empty() {
                                                html! {}
                                            } else {
                                                html! { <span class="section">{ format!(" › {}", c.section) }</span> }
                                            } }
                                            <small>{ format!(" · updated {}", day(&c.last_updated)) }</small>
                                        </li>
                                    }) }
                                </ol>
                            </>
                        }
                    } }
                    <div class="feedback">
                        { if rated.is_some() {
                            html! { <p>{ "Thanks for the feedback." }</p> }
                        } else {
                            html! {
                                <>
                                    <button onclick={up}>{ "Helpful" }</button>
                                    <button onclick={down}>{ "Not helpful" }</button>
                                </>
                            }
                        } }
                    </div>
                </section>
            }
        }
    }
}

fn main() {
    yew::Renderer::<App>::new().render();
}
