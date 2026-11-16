package v1

import (
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
)

var (
	_bytesRange   = regexp.MustCompile(`^ *bytes=`)
	_digits       = regexp.MustCompile(`^\d+$`)
	_noCacheToken = regexp.MustCompile(`(?:^|,)\s*?no-cache\s*?(?:,|$)`)
)

const (
	_rangeInvalid       = -2
	_rangeUnsatisfiable = -1

	_bom      = '\uFEFF'
	_nextLine = '\u0085'
)

type byteRange struct {
	start, end int64
}

// parseRange is range-parser 1.3.0 with combine; code is -2 for a malformed header and -1 when
// no range is satisfiable.
func parseRange(size int64, header string) (ranges []byteRange, code int) {
	index := strings.IndexByte(header, '=')
	if index == -1 {
		return nil, _rangeInvalid
	}

	for part := range strings.SplitSeq(header[index+1:], ",") {
		r, ok, valid := parseRangePart(size, part)
		if !valid {
			return nil, _rangeInvalid
		}

		if ok {
			ranges = append(ranges, r)
		}
	}

	if len(ranges) == 0 {
		return nil, _rangeUnsatisfiable
	}

	return combineRanges(ranges), 0
}

// parseRangePart reads one "start-end"; valid is false when it is malformed and ok is false when
// it is not satisfiable.
func parseRangePart(size int64, part string) (r byteRange, ok, valid bool) {
	dash := strings.IndexByte(part, '-')
	if dash == -1 {
		return byteRange{}, false, false
	}

	startStr, endStr := jsTrim(part[:dash]), jsTrim(part[dash+1:])
	start, end := parsePos(startStr), parsePos(endStr)

	switch {
	case startStr == "":
		start, end = float64(size)-end, float64(size-1)
	case endStr == "":
		end = float64(size - 1)
	}

	end = math.Min(end, float64(size-1))

	if math.IsNaN(start) || math.IsNaN(end) {
		return byteRange{}, false, false
	}

	if start > end || start < 0 {
		return byteRange{}, false, true
	}

	return byteRange{int64(start), int64(end)}, true, true
}

// combineRanges merges overlapping and adjacent ranges, kept in the order of their first range.
func combineRanges(ranges []byteRange) []byteRange {
	type indexed struct {
		byteRange
		index int
	}

	ordered := make([]indexed, len(ranges))
	for i, r := range ranges {
		ordered[i] = indexed{r, i}
	}

	slices.SortStableFunc(ordered, func(a, b indexed) int { return int(a.start - b.start) })

	j := 0
	for i := 1; i < len(ordered); i++ {
		r, cur := ordered[i], &ordered[j]

		switch {
		case r.start > cur.end+1:
			j++
			ordered[j] = r
		case r.end > cur.end:
			cur.end = r.end
			cur.index = min(cur.index, r.index)
		}
	}

	ordered = ordered[:j+1]
	slices.SortStableFunc(ordered, func(a, b indexed) int { return a.index - b.index })

	out := make([]byteRange, len(ordered))
	for i, o := range ordered {
		out[i] = o.byteRange
	}

	return out
}

func parsePos(s string) float64 {
	if !_digits.MatchString(s) {
		return math.NaN()
	}

	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}

	return n
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case _bom:
			return true
		case _nextLine:
			return false
		default:
			return unicode.IsSpace(r)
		}
	})
}

// parseTokenList splits a comma-separated header on commas and leading spaces.
func parseTokenList(s string) []string {
	var list []string

	start, end := 0, 0

	for i := range len(s) {
		switch s[i] {
		case ' ':
			if start == end {
				start, end = i+1, i+1
			}
		case ',':
			if start != end {
				list = append(list, s[start:end])
			}

			start, end = i+1, i+1
		default:
			end = i + 1
		}
	}

	if start != end {
		list = append(list, s[start:end])
	}

	return list
}

// joined is a request header as Node exposes it: repeated values joined with ", ".
func joined(r *http.Request, name string) string {
	return strings.Join(r.Header.Values(name), ", ")
}

func parseHTTPDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}

	t, err := http.ParseTime(s)

	return t, err == nil
}

func etagMatches(tokens []string, etag string) bool {
	return slices.ContainsFunc(tokens, func(m string) bool {
		return m == etag || m == "W/"+etag || "W/"+m == etag
	})
}

// preconditionFailed is send isPreconditionFailure (If-Match, If-Unmodified-Since).
func preconditionFailed(r *http.Request, etag string, modified time.Time) bool {
	if match := joined(r, "If-Match"); match != "" {
		return match != "*" && !etagMatches(parseTokenList(match), etag)
	}

	if since, ok := parseHTTPDate(r.Header.Get("If-Unmodified-Since")); ok {
		return modified.After(since)
	}

	return false
}

// fresh is the fresh 2.0.0 module (If-None-Match, If-Modified-Since, Cache-Control: no-cache).
func fresh(r *http.Request, etag string, modified time.Time) bool {
	modifiedSince, noneMatch := r.Header.Get("If-Modified-Since"), joined(r, "If-None-Match")
	if modifiedSince == "" && noneMatch == "" {
		return false
	}

	if cc := joined(r, "Cache-Control"); cc != "" && _noCacheToken.MatchString(cc) {
		return false
	}

	if noneMatch != "" {
		return noneMatch == "*" || etagMatches(parseTokenList(noneMatch), etag)
	}

	since, ok := parseHTTPDate(modifiedSince)

	return ok && !modified.After(since)
}

// rangeFresh is send isRangeFresh (If-Range).
func rangeFresh(r *http.Request, etag string, modified time.Time) bool {
	ifRange := joined(r, "If-Range")
	if ifRange == "" {
		return true
	}

	if strings.Contains(ifRange, `"`) {
		return strings.Contains(ifRange, etag)
	}

	since, ok := parseHTTPDate(ifRange)

	return ok && !modified.After(since)
}

// conditional answers 412 or 304 for a conditional GET; done is false when the file is sent.
func conditional(w http.ResponseWriter, r *http.Request, etag string, modified time.Time) (done bool, err error) {
	if joined(r, "If-Match") == "" && r.Header.Get("If-Unmodified-Since") == "" &&
		joined(r, "If-None-Match") == "" && r.Header.Get("If-Modified-Since") == "" {
		return false, nil
	}

	if preconditionFailed(r, etag, modified) {
		return true, httperr.New(http.StatusPreconditionFailed, "Precondition Failed")
	}

	if !fresh(r, etag, modified) {
		return false, nil
	}

	for _, k := range []string{"Content-Encoding", "Content-Language", "Content-Length", "Content-Range", "Content-Type"} {
		w.Header().Del(k)
	}

	w.WriteHeader(http.StatusNotModified)

	return true, nil
}

func contentRange(size int64, r *byteRange) string {
	if r == nil {
		return "bytes */" + strconv.FormatInt(size, 10)
	}

	return "bytes " + strconv.FormatInt(r.start, 10) + "-" + strconv.FormatInt(r.end, 10) + "/" + strconv.FormatInt(size, 10)
}

// send writes fh as send 1.2.1 does after its headers (ETag, Last-Modified, Content-Type, ...)
// are set: 412 / 304 for conditional requests, 416 or a single 206 range, else the whole file.
func send(w http.ResponseWriter, r *http.Request, fh *os.File, size int64, etag string, modified time.Time) error {
	h := w.Header()

	if done, err := conditional(w, r, etag, modified); done {
		return err
	}

	offset, length, status := int64(0), size, http.StatusOK

	if header := joined(r, "Range"); _bytesRange.MatchString(header) {
		ranges, code := parseRange(size, header)
		if !rangeFresh(r, etag, modified) {
			code = _rangeInvalid
		}

		if code == _rangeUnsatisfiable {
			h.Set("Content-Range", contentRange(size, nil))

			return httperr.New(http.StatusRequestedRangeNotSatisfiable, "Range Not Satisfiable")
		}

		if code != _rangeInvalid && len(ranges) == 1 {
			status = http.StatusPartialContent

			h.Set("Content-Range", contentRange(size, &ranges[0]))
			offset, length = ranges[0].start, ranges[0].end-ranges[0].start+1
		}
	}

	h.Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(status)

	if r.Method == http.MethodHead {
		return nil
	}

	_, _ = io.Copy(w, io.NewSectionReader(fh, offset, length))

	return nil
}
