package clean

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// Structured role filtering belongs to readers. These wrappers also occur
// inside visible user records from lifecycle hooks or harness setup.
var wrapperNames = []string{"system-reminder", "local-command-caveat", "command-name", "command-message", "command-args", "local-command-stdout", "environment_context", "INSTRUCTIONS", "environment_instructions"}
var wrappers = func() []*regexp.Regexp {
	var rules []*regexp.Regexp
	for _, tag := range wrapperNames {
		rules = append(rules, regexp.MustCompile("(?is)<"+tag+">.*?(?:</"+tag+">|$)"))
	}
	return rules
}()

var (
	secretFlag = regexp.MustCompile(`(?i)(?:^|\s)--(?:[a-z0-9_-]*(?:token|secret|password|passwd|passphrase|api[_-]?key)[a-z0-9_-]*)\s+\S`)

	secretKey   = regexp.MustCompile(`(?i)(^|[^a-z0-9_-])(--)?["']?([a-z0-9_-]*(token|secret|password|passwd|passphrase|passcode|api[_-]?key|authorization|cookie)[a-z0-9_-]*|pass|(?:db|ssh|pg|mysql|mfa|otp|auth|login|sudo|proxy|app|service|account|user|admin)[_-]?pass(?:[_-](?:b64|file|path|value|secret))?|pgpassfile|database[_-]?url|redis[_-]?url|dsn|session[_-]?id|sid)["']?[ \t]*(:|=|\s+--)`)
	privateKey  = regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	asset       = regexp.MustCompile(`(?i)\[Beam asset omitted: (file|image|document|video|audio)(?:[^\]\r\n]*)\]|\[Image(?: #[0-9]+)?: source: [^\]\r\n]*\]`)
	urlPattern  = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`)
	datePattern = regexp.MustCompile(`^(?:[0-9]{4}[-./][0-9]{1,2}[-./][0-9]{1,2}|[0-9]{1,2}[-./][0-9]{1,2}[-./][0-9]{4})$`)
	phone       = regexp.MustCompile(`(?:\+?[0-9][0-9 ().-]{7,}[0-9])`)
	quotedPath  = regexp.MustCompile(`(?i)(?:"(?:file:/|~/|\.{1,2}[/\\]|/|[a-z]:[/\\]|\\\\)[^"\n]*"|'(?:file:/|~/|\.{1,2}[/\\]|/|[a-z]:[/\\]|\\\\)[^'\n]*')`)
	localPath   = regexp.MustCompile(`(?im)(^|[\s"{(\[=:,;>])(?:file:/+|~/|\.{1,2}[/\\]|/|[a-z]:[/\\]|\\\\)[^\s<>"'\])},;]+`)
	rules       = []struct {
		re *regexp.Regexp
		to string
	}{
		{regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[a-z0-9._~+/=-]{6,}`), "[REDACTED_AUTH]"},
		{regexp.MustCompile(`\b(?:sk-(?:ant-)?|(?:sk|rk)_(?:live|test)_|whsec_|npm_|glpat-|gh[pousr]_|github_pat_|xox[baprs]-|AIza|AKIA)[A-Za-z0-9_-]{12,}\b`), "[REDACTED_KEY]"},
		{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`), "[REDACTED_TOKEN]"},
		{regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b`), "[REDACTED_EMAIL]"},
		{regexp.MustCompile(`\b(?:[a-fA-F0-9]{32,}|[A-Za-z0-9_+/=-]{40,})\b`), "[REDACTED_OPAQUE]"},
	}
)

func cleanText(s string, mask *regexp.Regexp, r *Report) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
	if strings.Contains(s, "<") {
		for _, re := range wrappers {
			s = re.ReplaceAllStringFunc(s, func(string) string { r.Omissions++; return "" })
		}
	}
	if strings.HasPrefix(s, "# AGENTS.md instructions for ") || strings.HasPrefix(s, "Knowledge cutoff:") || strings.HasPrefix(s, "You are Codex,") {
		return ""
	}
	s = asset.ReplaceAllStringFunc(s, func(match string) string {
		r.LossyConversions++
		kind := strings.ToLower(asset.FindStringSubmatch(match)[1])
		if kind == "" {
			kind = "image"
		}
		return "[Beam asset omitted: " + kind + "]"
	})
	if mask != nil {
		s = mask.ReplaceAllStringFunc(s, func(string) string { r.Redactions++; return "[REDACTED]" })
	}
	replace := func(re *regexp.Regexp, v string) {
		s = re.ReplaceAllStringFunc(s, func(string) string { r.Redactions++; return v })
	}
	replace(privateKey, "[REDACTED_PRIVATE_KEY]")
	s = redactLines(s, r)
	for _, rule := range rules {
		replace(rule.re, rule.to)
	}
	s = phone.ReplaceAllStringFunc(s, func(v string) string {
		if datePattern.MatchString(v) {
			return v
		}
		r.Redactions++
		return "[REDACTED_PHONE]"
	})
	// Preserve public links unless credentials or encoded local path values
	// occur in them. Never fetch a URL to determine how to display it.
	if !strings.ContainsAny(s, `/\`) {
		return strings.TrimSpace(s)
	}
	var out strings.Builder
	end := 0
	for _, loc := range urlPattern.FindAllStringIndex(s, -1) {
		out.WriteString(cleanPaths(s[end:loc[0]], r))
		link := s[loc[0]:loc[1]]
		u, err := url.Parse(link)
		if err != nil || u.User != nil || unsafeQuery(u) {
			r.Redactions++
			out.WriteString("[REDACTED_URL]")
		} else {
			out.WriteString(link)
		}
		end = loc[1]
	}
	out.WriteString(cleanPaths(s[end:], r))
	return strings.TrimSpace(out.String())
}

func cleanPaths(s string, r *Report) string {
	s = quotedPath.ReplaceAllStringFunc(s, func(string) string { r.Redactions++; return "[LOCAL_PATH]" })
	return localPath.ReplaceAllStringFunc(s, func(v string) string {
		r.Redactions++
		m := localPath.FindStringSubmatch(v)
		return m[1] + "[LOCAL_PATH]"
	})
}

func unsafeQuery(u *url.URL) bool {
	for _, part := range []string{u.RawQuery, u.Fragment} {
		for range 4 {
			decoded, err := url.QueryUnescape(part)
			if err != nil {
				return true
			}
			if decoded == part {
				break
			}
			part = decoded
		}
		if strings.Contains(part, "%") || localPath.MatchString(part) || secretKey.MatchString(part) {
			return true
		}
	}
	return false
}

func redactLines(s string, r *Report) string {
	// Both secret-key assignments and flags require one of these delimiters.
	// Skip splitting and matching prose that cannot start a redacted line.
	if !strings.ContainsAny(s, ":=") && !strings.Contains(s, "--") {
		return s
	}
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !secretKey.MatchString(line) && !secretFlag.MatchString(line) {
			out = append(out, line)
			continue
		}
		r.Redactions++
		out = append(out, "[REDACTED_SECRET_LINE]")
		trim := strings.TrimSpace(line)
		multiline := strings.HasSuffix(trim, "|") || strings.HasSuffix(trim, ">") || strings.HasSuffix(trim, "|-") || strings.HasSuffix(trim, ">-") || strings.HasSuffix(trim, "|+") || strings.HasSuffix(trim, ">+")
		expect := strings.HasSuffix(trim, ":") || strings.HasSuffix(trim, "=") || strings.HasSuffix(trim, "\\") || strings.HasSuffix(trim, "[") || strings.HasSuffix(trim, "{")
		if !multiline && !expect {
			continue
		}
		indent := len(line) - len(strings.TrimLeftFunc(line, unicode.IsSpace))
		for i+1 < len(lines) {
			next := lines[i+1]
			nextIndent := len(next) - len(strings.TrimLeftFunc(next, unicode.IsSpace))
			if strings.TrimSpace(next) != "" && nextIndent <= indent && !expect {
				break
			}
			i++
			if strings.TrimSpace(next) != "" {
				expect = false
			}
		}
	}
	return strings.Join(out, "\n")
}
