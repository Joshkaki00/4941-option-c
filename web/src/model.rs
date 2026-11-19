//! Wire types and pure helpers for the Ask the Handbook API.
//! No browser APIs here, so these run under `cargo test` on the host.

use serde::{Deserialize, Serialize};

/// Matches the server's maxQuestionRunes (internal/httpapi).
pub const MAX_QUESTION_CHARS: usize = 1000;

#[derive(Serialize, Debug, PartialEq)]
pub struct NewQuestion<'a> {
    pub content: &'a str,
    pub stream: bool,
}

#[derive(Serialize, Debug, PartialEq)]
pub struct FeedbackBody {
    pub rating: i8,
    pub comment: String,
}

#[derive(Deserialize, Debug, PartialEq)]
pub struct ConversationCreated {
    pub id: String,
}

#[derive(Deserialize, Clone, Debug, PartialEq)]
pub struct Citation {
    pub rank: u32,
    pub chunk_id: String,
    pub document_title: String,
    pub section: String,
    pub last_updated: String,
}

#[derive(Deserialize, Clone, Debug, PartialEq)]
pub struct Answer {
    pub message_id: String,
    pub outcome: String,
    pub answer: String,
    #[serde(default)]
    pub handoff: String,
    #[serde(default)]
    pub citations: Vec<Citation>,
}

/// Payload of an SSE `token` event: one piece of the answer text.
#[derive(Deserialize, Debug, PartialEq)]
pub struct TokenEvent {
    pub text: String,
}

/// Payload of an SSE `error` event.
#[derive(Deserialize, Debug, PartialEq)]
pub struct ErrorEvent {
    pub error: String,
}

/// Human label for an outcome returned by the API.
pub fn outcome_label(outcome: &str) -> &'static str {
    match outcome {
        "answered" => "Answered from the handbook",
        "declined_no_source" => "Not in the handbook",
        "routed_to_human" => "Please contact a person",
        _ => "Unknown",
    }
}

/// True when a question is non-empty after trimming and within the server limit.
pub fn is_valid_question(question: &str) -> bool {
    let trimmed = question.trim();
    !trimmed.is_empty() && trimmed.chars().count() <= MAX_QUESTION_CHARS
}

/// Authorization header value. Surrounding whitespace in the token is ignored.
pub fn bearer(token: &str) -> String {
    format!("Bearer {}", token.trim())
}

/// One Server-Sent Event: the `event:` name (default "message") and its joined `data:` lines.
#[derive(Debug, PartialEq, Clone)]
pub struct SseEvent {
    pub event: String,
    pub data: String,
}

/// Splits complete SSE events off `buf` and returns them with the unfinished remainder.
pub fn parse_sse(buf: &str) -> (Vec<SseEvent>, String) {
    let normalized = buf.replace("\r\n", "\n");
    let mut events = Vec::new();
    let mut rest = normalized.as_str();
    while let Some(end) = rest.find("\n\n") {
        let block = &rest[..end];
        rest = &rest[end + 2..];
        if let Some(event) = parse_block(block) {
            events.push(event);
        }
    }
    (events, rest.to_string())
}

fn parse_block(block: &str) -> Option<SseEvent> {
    let mut name = None;
    let mut data: Vec<&str> = Vec::new();
    for line in block.lines() {
        if line.is_empty() || line.starts_with(':') {
            continue;
        }
        let (field, value) = match line.split_once(':') {
            Some((f, v)) => (f, v.strip_prefix(' ').unwrap_or(v)),
            None => (line, ""),
        };
        match field {
            "event" => name = Some(value.to_string()),
            "data" => data.push(value),
            _ => {}
        }
    }
    if data.is_empty() {
        return None;
    }
    Some(SseEvent {
        event: name.unwrap_or_else(|| "message".to_string()),
        data: data.join("\n"),
    })
}

/// Decodes as much of `pending` as is complete UTF-8, leaving an incomplete trailing
/// character in `pending` for the next chunk.
pub fn take_utf8(pending: &mut Vec<u8>) -> String {
    match std::str::from_utf8(pending) {
        Ok(s) => {
            let out = s.to_owned();
            pending.clear();
            out
        }
        Err(e) if e.error_len().is_none() => {
            // Incomplete character at the end: keep it for the next chunk.
            let valid = e.valid_up_to();
            let out = String::from_utf8_lossy(&pending[..valid]).into_owned();
            pending.drain(..valid);
            out
        }
        Err(_) => {
            // Invalid bytes mid-stream: replace them rather than stall the answer.
            let out = String::from_utf8_lossy(pending).into_owned();
            pending.clear();
            out
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sse_splits_complete_events_and_keeps_remainder() {
        let (events, rest) =
            parse_sse("event: token\ndata: {\"text\":\"Hi\"}\n\nevent: done\ndata: {");
        assert_eq!(
            events,
            vec![SseEvent {
                event: "token".into(),
                data: "{\"text\":\"Hi\"}".into()
            }]
        );
        assert_eq!(rest, "event: done\ndata: {");
    }

    #[test]
    fn sse_joins_multi_line_data() {
        let (events, _) = parse_sse("event: x\ndata: a\ndata: b\n\n");
        assert_eq!(events[0].data, "a\nb");
    }

    #[test]
    fn sse_defaults_event_name_to_message() {
        let (events, _) = parse_sse("data: hi\n\n");
        assert_eq!(events[0].event, "message");
    }

    #[test]
    fn sse_ignores_comments_and_blocks_without_data() {
        let (events, rest) = parse_sse(": keepalive\r\n\r\n");
        assert!(events.is_empty());
        assert_eq!(rest, "");
    }

    #[test]
    fn sse_handles_crlf_line_endings() {
        let (events, _) = parse_sse("event: token\r\ndata: {\"text\":\"x\"}\r\n\r\n");
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].event, "token");
    }

    #[test]
    fn utf8_passes_ascii_through() {
        let mut pending = b"plain text".to_vec();
        assert_eq!(take_utf8(&mut pending), "plain text");
        assert!(pending.is_empty());
    }

    #[test]
    fn utf8_waits_for_split_multibyte_character() {
        // "é" is 0xC3 0xA9. Deliver the first byte alone, then the second.
        let mut pending = vec![0xC3];
        assert_eq!(take_utf8(&mut pending), "");
        assert_eq!(pending, vec![0xC3]);
        pending.push(0xA9);
        assert_eq!(take_utf8(&mut pending), "é");
        assert!(pending.is_empty());
    }

    const ANSWERED: &str = r#"{
        "message_id": "m-1",
        "outcome": "answered",
        "answer": "Full-time employees get 15 days [1].",
        "citations": [{
            "rank": 1,
            "chunk_id": "c-1",
            "document_title": "PTO Policy",
            "section": "Accrual",
            "last_updated": "2026-03-01T00:00:00Z"
        }]
    }"#;

    #[test]
    fn parses_answered_response_with_citations() {
        let a: Answer = serde_json::from_str(ANSWERED).unwrap();
        assert_eq!(a.outcome, "answered");
        assert_eq!(a.citations.len(), 1);
        assert_eq!(a.citations[0].document_title, "PTO Policy");
        assert_eq!(a.handoff, "", "missing handoff defaults to empty");
    }

    #[test]
    fn decline_without_citations_still_parses() {
        let raw = r#"{"message_id":"m-2","outcome":"declined_no_source","answer":"I don't know."}"#;
        let a: Answer = serde_json::from_str(raw).unwrap();
        assert!(a.citations.is_empty());
    }

    #[test]
    fn rejects_response_missing_message_id() {
        let raw = r#"{"outcome":"answered","answer":"x"}"#;
        assert!(serde_json::from_str::<Answer>(raw).is_err());
    }

    #[test]
    fn parses_conversation_id() {
        let c: ConversationCreated = serde_json::from_str(r#"{"id":"abc"}"#).unwrap();
        assert_eq!(c.id, "abc");
    }

    #[test]
    fn question_body_uses_content_and_stream_fields() {
        let body = NewQuestion {
            content: "PTO?",
            stream: false,
        };
        let json = serde_json::to_value(&body).unwrap();
        assert_eq!(
            json,
            serde_json::json!({"content": "PTO?", "stream": false})
        );
    }

    #[test]
    fn feedback_body_matches_server_fields() {
        let body = FeedbackBody {
            rating: -1,
            comment: String::new(),
        };
        let json = serde_json::to_value(&body).unwrap();
        assert_eq!(json, serde_json::json!({"rating": -1, "comment": ""}));
    }

    #[test]
    fn outcome_labels_cover_all_api_outcomes() {
        assert_eq!(outcome_label("answered"), "Answered from the handbook");
        assert_eq!(outcome_label("declined_no_source"), "Not in the handbook");
        assert_eq!(outcome_label("routed_to_human"), "Please contact a person");
        assert_eq!(outcome_label("something_new"), "Unknown");
    }

    #[test]
    fn question_must_be_non_blank_and_within_limit() {
        assert!(!is_valid_question(""));
        assert!(!is_valid_question("   \n\t "));
        assert!(is_valid_question("  How many PTO days?  "));
        let max = "a".repeat(MAX_QUESTION_CHARS);
        assert!(is_valid_question(&max));
        assert!(!is_valid_question(&"a".repeat(MAX_QUESTION_CHARS + 1)));
    }

    #[test]
    fn question_length_counts_characters_not_bytes() {
        // 1000 two-byte characters are within the limit even though they are 2000 bytes.
        assert!(is_valid_question(&"é".repeat(MAX_QUESTION_CHARS)));
    }

    #[test]
    fn bearer_trims_token_and_prefixes_scheme() {
        assert_eq!(bearer("  dev-alice \n"), "Bearer dev-alice");
    }
}
