package bridge

import "strings"

// quoteTitleArg preserves Unicode, spaces, quotes and trailing backslashes as
// one Windows argv element. The batch expands this via delayed expansion only;
// it must never use CALL or percent expansion to reinterpret the title.
func quoteTitleArg(title string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range title {
		if r == '\\' {
			slashes++
			continue
		}
		count := slashes
		if r == '"' {
			count = slashes*2 + 1
		}
		b.WriteString(strings.Repeat(`\`, count))
		b.WriteRune(r)
		slashes = 0
	}
	b.WriteString(strings.Repeat(`\`, slashes*2))
	b.WriteByte('"')
	return b.String()
}
