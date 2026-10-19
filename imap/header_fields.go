package imap

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-message"
)

// headerFieldsLiteral answers BODY[HEADER.FIELDS (...)] and HEADER.FIELDS.NOT
// with exactly the requested header fields, case-insensitively, as RFC 3501
// requires. It bypasses message.CreateWriter on purpose: that writer adds a
// Mime-Version line of its own, and upstream also returned the whole header,
// which broke Cypht's "date" sort (it reads the first line after the literal
// as the Date).
func headerFieldsLiteral(h message.Header, section *imap.BodySectionName) (imap.Literal, error) {
	want := make(map[string]bool, len(section.Fields))
	for _, f := range section.Fields {
		want[strings.ToLower(f)] = true
	}
	b := new(bytes.Buffer)
	hf := h.Fields()
	for hf.Next() {
		if want[strings.ToLower(hf.Key())] != section.NotFields {
			fmt.Fprintf(b, "%s: %s\r\n", hf.Key(), hf.Value())
		}
	}
	b.WriteString("\r\n")
	var l imap.Literal = b
	if section.Partial != nil {
		l = bytes.NewReader(section.ExtractPartial(b.Bytes()))
	}
	return l, nil
}
