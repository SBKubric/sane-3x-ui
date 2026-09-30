package service

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// SubUserSearchLimit is the most users Search returns.
const SubUserSearchLimit = 20

// The ranks of a search hit, best first (docs/spec/users.md §5, decision
// #186 point 6).
const (
	searchExact     = iota // the whole field, ignoring case
	searchPrefix           // the field starts with the query
	searchSubstring        // the field contains the query
	searchTypo             // a word or the field is a few edits away
)

// Search is the one fuzzy search of the bot and the users page: at most
// SubUserSearchLimit users, best first. A field matches the query exactly,
// at its start, anywhere, or with typos — per field and per word, one edit
// in a word of up to five characters, two in a longer one — ignoring case.
// The fields are the user's name, subId (regular users only), contact
// email and Telegram @nick (of the account its tg_id points at, §11), and
// the names of its clients (xray emails, tunnel client names); a technical
// user is found by its name and its clients. A Telegram id — the user's or a
// client's — matches only whole. A query that starts with '@' is a nick and
// searches the nicks alone.
//
// Short queries stay precise: a single character matches only whole or at
// the start of a field, typos count from three characters on, and a query of
// digits never matches with typos (a tg_id a digit off is somebody else).
// Hits of one rank come by distance, then by name. None is not an error.
//
// It reads the index and the nicks once (one query each) and matches in
// memory: one pass over thousands of users costs no more than List.
func (s *SubUserService) Search(query string) ([]*SubUserView, error) {
	idx, err := s.synced()
	if err != nil {
		return nil, err
	}
	q := newSearchQuery(query)
	if q.text == "" {
		return nil, nil
	}
	nicks, err := (&TgAccountService{}).UserNicks()
	if err != nil {
		return nil, err
	}

	best := map[string]searchScore{}
	consider := func(key string, sc searchScore) {
		if old, seen := best[key]; !seen || sc.less(old) {
			best[key] = sc
		}
	}
	for key, u := range idx.users {
		if q.tgId > 0 && u.TgId == q.tgId {
			consider(key, searchScore{rank: searchExact})
		}
		if nick := nicks[u.TgId]; u.TgId != 0 && nick != "" {
			if sc, ok := q.match(strings.TrimPrefix(nick, "@")); ok {
				consider(key, sc)
			}
		}
		if q.nickOnly {
			continue
		}
		fields := []string{u.Name, u.ContactEmail}
		if !u.IsTechnical() {
			fields = append(fields, u.SubId)
		}
		for _, f := range fields {
			if sc, ok := q.match(f); ok {
				consider(key, sc)
			}
		}
	}
	for _, c := range idx.clients {
		if idx.users[c.owner] == nil {
			continue
		}
		if q.tgId > 0 && c.TgId == q.tgId {
			consider(c.owner, searchScore{rank: searchExact})
		}
		if q.nickOnly {
			continue
		}
		if sc, ok := q.match(c.Name); ok {
			consider(c.owner, sc)
		}
	}

	hits := make([]*model.SubUser, 0, len(best))
	for key := range best {
		hits = append(hits, idx.users[key])
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := best[hits[i].SubId], best[hits[j].SubId]
		if a != b {
			return a.less(b)
		}
		if ni, nj := strings.ToLower(hits[i].Name), strings.ToLower(hits[j].Name); ni != nj {
			return ni < nj
		}
		return hits[i].SubId < hits[j].SubId
	})
	if len(hits) > SubUserSearchLimit {
		hits = hits[:SubUserSearchLimit]
	}
	out := make([]*SubUserView, 0, len(hits))
	for _, u := range hits {
		out = append(out, idx.view(u))
	}
	return out, nil
}

// searchScore is how well a user matched: its best rank, and for a typo the
// number of edits.
type searchScore struct {
	rank  int
	edits int
}

func (a searchScore) less(b searchScore) bool {
	if a.rank != b.rank {
		return a.rank < b.rank
	}
	return a.edits < b.edits
}

// searchQuery is a query as the matcher reads it.
type searchQuery struct {
	text     string   // lower case, trimmed; the nick without its '@'
	words    []string // text split at anything but letters and digits
	tgId     int64    // the query as a Telegram id; 0 unless all digits
	nickOnly bool     // the query started with '@'
	typos    bool     // the query may match with typos
}

func newSearchQuery(raw string) searchQuery {
	text := strings.ToLower(strings.TrimSpace(raw))
	q := searchQuery{}
	if rest, ok := strings.CutPrefix(text, "@"); ok {
		q.nickOnly, text = true, strings.TrimSpace(rest)
	}
	q.text = text
	q.words = searchWords(text)
	digits := text != "" && strings.IndexFunc(text, func(r rune) bool { return r < '0' || r > '9' }) < 0
	if digits && !q.nickOnly {
		q.tgId, _ = strconv.ParseInt(text, 10, 64)
	}
	q.typos = !digits && utf8.RuneCountInString(text) >= searchTypoMinLen
	return q
}

// searchTypoMinLen is the shortest query, and query word, that may match with
// typos: one edit in a word of one or two characters makes it anything.
const searchTypoMinLen = 3

// match scores one field against the query.
func (q searchQuery) match(field string) (searchScore, bool) {
	f := strings.ToLower(strings.TrimSpace(field))
	if f == "" {
		return searchScore{}, false
	}
	switch {
	case f == q.text:
		return searchScore{rank: searchExact}, true
	case strings.HasPrefix(f, q.text):
		return searchScore{rank: searchPrefix}, true
	case utf8.RuneCountInString(q.text) > 1 && strings.Contains(f, q.text):
		return searchScore{rank: searchSubstring}, true
	}
	if !q.typos {
		return searchScore{}, false
	}
	best := -1
	// The whole field, as the query is typed: "ivanpetrov" for "ivan-petrov".
	if d, ok := boundedLevenshtein(q.text, f, searchMaxEdits(q.text)); ok {
		best = d
	}
	// Word by word: every word of the query is a word of the field, or a few
	// edits from one; short query words must be whole or start a word.
	fieldWords := searchWords(f)
	total := 0
	for _, w := range q.words {
		wordBest := -1
		for _, fw := range fieldWords {
			var d int
			var ok bool
			if utf8.RuneCountInString(w) < searchTypoMinLen {
				d, ok = 0, strings.HasPrefix(fw, w)
			} else {
				d, ok = boundedLevenshtein(w, fw, searchMaxEdits(w))
			}
			if ok && (wordBest < 0 || d < wordBest) {
				wordBest = d
			}
		}
		if wordBest < 0 {
			total = -1
			break
		}
		total += wordBest
	}
	if total >= 0 && len(q.words) > 0 && (best < 0 || total < best) {
		best = total
	}
	if best < 0 {
		return searchScore{}, false
	}
	return searchScore{rank: searchTypo, edits: best}, true
}

// searchMaxEdits is the typos a word forgives: one up to five characters,
// two beyond.
func searchMaxEdits(word string) int {
	if utf8.RuneCountInString(word) <= 5 {
		return 1
	}
	return 2
}

// searchWords splits s at anything but letters and digits.
func searchWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// boundedLevenshtein is the edit distance of a and b in runes when it is at
// most max; ok is false beyond, found without filling the whole table.
func boundedLevenshtein(a, b string, max int) (int, bool) {
	ra, rb := []rune(a), []rune(b)
	if d := len(ra) - len(rb); d > max || -d > max {
		return 0, false
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			rowMin = min(rowMin, cur[j])
		}
		if rowMin > max {
			return 0, false
		}
		prev, cur = cur, prev
	}
	if d := prev[len(rb)]; d <= max {
		return d, true
	}
	return 0, false
}
