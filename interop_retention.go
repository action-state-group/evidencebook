// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RecordTypeRetentionCommitment is the record type of a retention
// commitment this book has issued.
const RecordTypeRetentionCommitment = "retention_commitment"

type retentionCommitment struct {
	subject Subject
	until   time.Time
}

// CommitRetention seals a retention commitment: a signed statement that
// subject stays answerable under this interaction until until (ER §9). The
// subject is a record, an exchange or a range: a digest or a position, never
// a correlation or the whole history (ERV A11). After until, a request for a
// subject the book no longer answers is refused retention_expired, never
// no_such_subject (ER §4.2 item 2).
func (b *Book) CommitRetention(ctx context.Context, subject Subject, until time.Time) (Record, error) {
	if !subject.valid() || (subject.Kind != SubjectRecord && subject.Kind != SubjectExchange && subject.Kind != SubjectRange) {
		return Record{}, fmt.Errorf("%w: a retention commitment names a record, an exchange or a range", ErrInvalid)
	}
	statement, err := toJCS(map[string]any{"subject": subject.value(), "until": profileTime(until)})
	if err != nil {
		return Record{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.appendLocked(ctx, Entry{RecordType: RecordTypeRetentionCommitment, EpistemicType: ProducerClaim, Links: []Link{}, Statement: json.RawMessage(statement)})
}

func readRetentionCommitment(statement []byte) (retentionCommitment, bool) {
	decoded, err := decodeStrictJSON(statement)
	fields, ok := decoded.(map[string]any)
	if err != nil || !ok {
		return retentionCommitment{}, false
	}
	subject, ok := parseSubject(fields["subject"])
	text, textOK := fields["until"].(string)
	until, timeOK := parseProfileTime(text)
	return retentionCommitment{subject: subject, until: until}, ok && textOK && timeOK
}
