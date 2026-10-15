// Package meter turns the usage lines the tenant iRules send into Prometheus counters and writes each
// Run:ai project's token total back onto the Project (and rolled up onto its Department).
//
// Wire format, one record per line (see bnk.TenantIRule):
//
//	project=<p> dept=<d> model=<m> prompt=<n> completion=<n> total=<n> code=<status>[ n=<count>]
//
// Fields are space-separated key=value pairs in any order. dept and model may be empty. n counts identical
// records folded into one line (the iRule folds 429 refusals); it defaults to 1.
package meter

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxLine is the longest line accepted; a well-formed record is far shorter.
const MaxLine = 512

// Bounds a single record must stay within; anything bigger is not from our iRule.
const (
	maxTokens = 10_000_000
	maxCount  = 1_000_000
)

var (
	nameRE  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	modelRE = regexp.MustCompile(`^[A-Za-z0-9._/:-]{0,128}$`)
)

// Record is one parsed usage line.
type Record struct {
	Project    string
	Department string
	Model      string
	Prompt     int64
	Completion int64
	Total      int64
	Code       int
	// Count is how many requests the line stands for (n=, default 1).
	Count int64
}

// ErrMalformed is wrapped by every parse failure.
var ErrMalformed = errors.New("malformed usage line")

func malformed(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, a...))
}

// Parse reads one line (without its newline). Unknown keys, duplicates, missing keys, bad names and
// out-of-range numbers are all rejected.
func Parse(line string) (Record, error) {
	line = strings.TrimRight(line, "\r")
	if len(line) > MaxLine {
		return Record{}, malformed("line longer than %d bytes", MaxLine)
	}
	fields := map[string]string{}
	for _, f := range strings.Fields(line) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return Record{}, malformed("field %q is not key=value", f)
		}
		switch k {
		case "project", "dept", "model", "prompt", "completion", "total", "code", "n":
		default:
			return Record{}, malformed("unknown key %q", k)
		}
		if _, dup := fields[k]; dup {
			return Record{}, malformed("duplicate key %q", k)
		}
		fields[k] = v
	}
	for _, k := range []string{"project", "dept", "model", "prompt", "completion", "total", "code"} {
		if _, ok := fields[k]; !ok {
			return Record{}, malformed("missing %q", k)
		}
	}
	r := Record{Project: fields["project"], Department: fields["dept"], Model: fields["model"], Count: 1}
	if !nameRE.MatchString(r.Project) {
		return Record{}, malformed("invalid project %q", r.Project)
	}
	if r.Department != "" && !nameRE.MatchString(r.Department) {
		return Record{}, malformed("invalid dept %q", r.Department)
	}
	if !modelRE.MatchString(r.Model) {
		return Record{}, malformed("invalid model %q", r.Model)
	}
	var err error
	num := func(k string, max int64) int64 {
		if err != nil {
			return 0
		}
		v, e := strconv.ParseInt(fields[k], 10, 64)
		if e != nil || v < 0 || v > max {
			err = malformed("%s=%q must be an integer in [0, %d]", k, fields[k], max)
		}
		return v
	}
	r.Prompt = num("prompt", maxTokens)
	r.Completion = num("completion", maxTokens)
	r.Total = num("total", 2*maxTokens)
	code := num("code", 599)
	if _, ok := fields["n"]; ok {
		r.Count = num("n", maxCount)
	}
	if err != nil {
		return Record{}, err
	}
	if code < 100 {
		return Record{}, malformed("code %d out of range", code)
	}
	if r.Count < 1 {
		return Record{}, malformed("n must be at least 1")
	}
	r.Code = int(code)
	// A response whose usage carried no total still counts what it did carry.
	if r.Total == 0 {
		r.Total = r.Prompt + r.Completion
	}
	return r, nil
}
