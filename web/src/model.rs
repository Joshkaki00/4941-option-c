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

#[cfg(test)]
mod tests {
    use super::*;

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
        let body = NewQuestion { content: "PTO?", stream: false };
        let json = serde_json::to_value(&body).unwrap();
        assert_eq!(json, serde_json::json!({"content": "PTO?", "stream": false}));
    }

    #[test]
    fn feedback_body_matches_server_fields() {
        let body = FeedbackBody { rating: -1, comment: String::new() };
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
