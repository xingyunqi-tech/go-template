package ratelimit

import "time"

// Subject kinds of a rule.
const (
	SubjectIP Subject = iota
	SubjectUser
	// SubjectUserOrIP counts signed-in requests per user and the others per IP.
	SubjectUserOrIP
	// SubjectAccount counts per normalized email or phone, checked by the use case.
	SubjectAccount
	// SubjectSessionOrIP counts per session found for the token, else per IP, checked by the use case.
	SubjectSessionOrIP
)

// Subject is what a rule counts requests by.
type Subject int

// SharedRule is a rule every service counts together (platform conventions §11.6).
type SharedRule struct {
	Rule    Rule
	Subject Subject
	// FailuresOnly rules count failed attempts and are reset on success.
	FailuresOnly bool
}

// Shared returns the shared rules keyed by name.
func Shared() map[string]SharedRule {
	rules := []SharedRule{
		{Rule: Rule{Name: "auth-login", Rate: 20, Period: time.Minute, Burst: 10}, Subject: SubjectIP, FailuresOnly: false},
		{Rule: Rule{Name: "auth-login-account", Rate: 10, Period: time.Hour, Burst: 5}, Subject: SubjectAccount, FailuresOnly: true},
		{Rule: Rule{Name: "auth-register", Rate: 10, Period: time.Hour, Burst: 3}, Subject: SubjectIP, FailuresOnly: false},
		{Rule: Rule{Name: "auth-mail", Rate: 5, Period: time.Hour, Burst: 3}, Subject: SubjectUserOrIP, FailuresOnly: false},
		{Rule: Rule{Name: "auth-sms", Rate: 10, Period: time.Hour, Burst: 5}, Subject: SubjectIP, FailuresOnly: false},
		{Rule: Rule{Name: "auth-mail-account", Rate: 3, Period: time.Hour, Burst: 2}, Subject: SubjectAccount, FailuresOnly: false},
		{Rule: Rule{Name: "auth-password-verify", Rate: 5, Period: 15 * time.Minute, Burst: 5}, Subject: SubjectUser, FailuresOnly: true},
		{Rule: Rule{Name: "auth-refresh", Rate: 30, Period: time.Minute, Burst: 10}, Subject: SubjectSessionOrIP, FailuresOnly: false},
	}

	out := make(map[string]SharedRule, len(rules))
	for _, r := range rules {
		out[r.Rule.Name] = r
	}

	return out
}

// PrivateIP is the {service}-ip rule: every /api/v1 request per IP.
func PrivateIP(service string) Rule {
	return Rule{Name: service + "-ip", Rate: 300, Period: time.Minute, Burst: 100}
}

// PrivateUser is the {service}-user rule: every signed-in request per user.
func PrivateUser(service string) Rule {
	return Rule{Name: service + "-user", Rate: 120, Period: time.Minute, Burst: 60}
}

// IPSubject is the subject ip:{IP}.
func IPSubject(subjectIP string) string {
	return "ip:" + subjectIP
}

// UserSubject is the subject u:{sub}.
func UserSubject(sub string) string {
	return "u:" + sub
}
