package main

import (
	"fmt"
	"io"
	"strings"
)

// confirmNumeric is fail-closed: only the complete line "1" approves an
// operation. 0, Enter, words, malformed values, and input errors cancel it.
// Passing an existing *bufio.Reader preserves buffered menu input.
func confirmNumeric(input io.Reader, out io.Writer, action string) bool {
	fmt.Fprintf(out, "\n%s\n  1 — Подтвердить\n  0 — Отмена\nВыбор: ", action)
	answer, err := sharedBufferedReader(input).ReadString('\n')
	return err == nil && strings.TrimSpace(answer) == "1"
}
