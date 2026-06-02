package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxRustSignalBytes = 2 << 20

func isRustTestPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	if !strings.HasSuffix(base, ".rs") {
		return false
	}
	return strings.HasPrefix(rel, "tests/") || strings.Contains(rel, "/tests/") ||
		strings.HasPrefix(rel, "benches/") || strings.Contains(rel, "/benches/")
}

func scanRustFileSignals(path, rel string, remainingRiskHints int) (hasTests bool, risks []string) {
	if remainingRiskHints <= 0 {
		remainingRiskHints = 0
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxRustSignalBytes {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	text := string(data)
	hasTests = strings.Contains(text, "#[test]") ||
		strings.Contains(text, "::test]") ||
		strings.Contains(text, "#[cfg(test)]")
	if remainingRiskHints == 0 {
		return hasTests, nil
	}
	for i, line := range strings.Split(text, "\n") {
		if len(risks) >= remainingRiskHints {
			break
		}
		if label := rustRiskLabel(line); label != "" {
			risks = append(risks, fmt.Sprintf("%s:%d %s", filepath.ToSlash(rel), i+1, label))
		}
	}
	return hasTests, risks
}

func rustRiskLabel(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "//") {
		return ""
	}
	switch {
	case strings.Contains(trimmed, "unsafe {") || strings.HasPrefix(trimmed, "unsafe fn ") ||
		strings.HasPrefix(trimmed, "unsafe impl ") || strings.Contains(trimmed, " unsafe {") ||
		strings.Contains(trimmed, " unsafe fn ") || strings.Contains(trimmed, " unsafe impl "):
		return "Rust unsafe boundary"
	case strings.Contains(trimmed, ".unwrap()"):
		return "Rust unwrap on reachable path"
	case strings.Contains(trimmed, ".expect("):
		return "Rust expect on reachable path"
	case strings.Contains(trimmed, "panic!("):
		return "Rust panic macro"
	case strings.Contains(trimmed, "todo!(") || strings.Contains(trimmed, "unimplemented!("):
		return "Rust incomplete code macro"
	case strings.Contains(trimmed, "let _ ="):
		return "Rust ignored result/value"
	default:
		return ""
	}
}
