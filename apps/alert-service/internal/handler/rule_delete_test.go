package handler

import (
	"errors"
	"net/http"
	"testing"
)

func TestRuleDeleteStatus(t *testing.T) {
	if code, _ := ruleDeleteStatus(nil); code != http.StatusNoContent {
		t.Errorf("nil error: code=%d, want 204", code)
	}
	if code, msg := ruleDeleteStatus(errors.New("rule not found")); code != http.StatusNotFound || msg != "Rule not found" {
		t.Errorf("not found: code=%d msg=%q", code, msg)
	}
	if code, msg := ruleDeleteStatus(errors.New("delete rule: boom")); code != http.StatusInternalServerError || msg != "Failed to delete rule" {
		t.Errorf("other error: code=%d msg=%q", code, msg)
	}
}
