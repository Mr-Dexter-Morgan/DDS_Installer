package main

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func TestNumericConfirmationOnlyExactOne(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  bool
	}{
		{"1\n", true}, {" 1 \r\n", true},
		{"0\n", false}, {"\n", false}, {"2\n", false},
		{"01\n", false}, {"1 2\n", false}, {"yes\n", false},
		{"УСТАНОВИТЬ\n", false}, {"ПОДГОТОВИТЬ\n", false},
		{"ВОССТАНОВИТЬ\n", false}, {"ПРОДОЛЖИТЬ\n", false},
		{"1", false}, {"", false},
	} {
		t.Run(strings.ReplaceAll(tt.input, "\n", "-newline-"), func(t *testing.T) {
			var output strings.Builder
			got := confirmNumeric(strings.NewReader(tt.input), &output, "Тестовое действие?")
			if got != tt.want {
				t.Fatalf("input %q: got %t want %t", tt.input, got, tt.want)
			}
			if !strings.Contains(output.String(), "1 — Подтвердить") || !strings.Contains(output.String(), "0 — Отмена") {
				t.Fatalf("not a numeric prompt: %q", output.String())
			}
		})
	}
}

func TestNumericConfirmPreservesBufferedMenuLine(t *testing.T) {
	b := bufio.NewReader(strings.NewReader("8\n1\n0\n"))
	if selectStage(b, io.Discard) != "8" {
		t.Fatal("menu selection missing")
	}
	if !confirmNumeric(b, io.Discard, "Подготовить?") {
		t.Fatal("expected yes")
	}
	if confirmNumeric(b, io.Discard, "Установить?") {
		t.Fatal("expected explicit cancellation")
	}
}
