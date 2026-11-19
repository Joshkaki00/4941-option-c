package main

import (
	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/eval"
)

// answerRequest builds the same request shape the HTTP API sends for a golden item.
func answerRequest(it eval.Item) answer.Request {
	return answer.Request{Question: it.Question, Audiences: it.Audiences}
}
