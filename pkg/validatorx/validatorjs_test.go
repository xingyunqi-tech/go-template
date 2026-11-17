package validatorx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func callValidatorJS(t *testing.T, name, value string, opts Value) (bool, error) {
	t.Helper()

	switch name {
	case "isEmail":
		return isEmail(value, optionsOf(opts))
	case "isURL":
		return isURL(value, optionsOf(opts)), nil
	case "isUUID":
		return isUUID(value, opts)
	case "isISO8601":
		return isISO8601(value, optionsOf(opts)), nil
	case "isLength":
		return isLength(value, optionsOf(opts)), nil
	case "isByteLength":
		return isByteLength(value, optionsOf(opts), !isUndefined(opts))
	case "isFQDN":
		return isFQDN(value, fqdnOptionsOf(optionsOf(opts))), nil
	case "isIP":
		return isIP(value, opts), nil
	}

	t.Fatalf("unknown function %s", name)

	return false, nil
}

func TestValidatorJSFunctionsMatchValidatorJS(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fn       string
		value    string
		options  string
		expected bool
	}{
		{"isEmail", "foo@bar.com", "", true},
		{"isEmail", "foo+bar@bar.com", "", true},
		{"isEmail", "hans.m\u7aefller@test.com", "", true},
		{"isEmail", "\"foobar\"@example.com", "", true},
		{"isEmail", "\"foo\\@bar\"@example.com", "", true},
		{"isEmail", "\"@x.com", "", true},
		{"isEmail", "invalidemail@", "", false},
		{"isEmail", "@invalid.com", "", false},
		{"isEmail", "foo@bar.com.", "", false},
		{"isEmail", "foo@_bar.com", "", false},
		{"isEmail", "test@email..com", "", false},
		{"isEmail", "a@b.com\n", "", false},
		{"isEmail", "test1@invalid.co\u00a0m", "", false},
		{"isEmail", "somename@\uff47\uff4d\uff41\uff49\uff4c.com", "", false},
		{"isEmail", "a@[127.0.0.1]", "", false},
		{"isEmail", "a@127.0.0.1", "", false},
		{"isEmail", "\U0001f600@x.com", "", false},
		{"isEmail", "a@\U0001f600.com", "", true},
		{"isEmail", "a@x.c\U0001f600", "", false},
		{"isEmail", "a@\u4f8b\u5b50.\u4e2d\u56fd", "", true},
		{"isEmail", "a@xn--fiqs8s.xn--fiqz9s", "", true},
		{"isEmail", "\u212a@x.com", "", true},
		{"isEmail", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa@x.com", "", true},
		{"isEmail", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa@x.com", "", false},
		{"isEmail", "a@bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.com", "", true},
		{"isEmail", "a@bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.com", "", false},
		{"isEmail", "Some Name <foo@bar.com>", "", false},
		{"isEmail", "Some Name <foo@bar.com>", "{\"allow_display_name\": true}", true},
		{"isEmail", "Some.Name <foo@bar.com>", "{\"allow_display_name\": true}", false},
		{"isEmail", "\"Some.Name\" <foo@bar.com>", "{\"allow_display_name\": true}", true},
		{"isEmail", "foo@bar.com", "{\"require_display_name\": true}", false},
		{"isEmail", "a@[127.0.0.1]", "{\"allow_ip_domain\": true}", true},
		{"isEmail", "\u00fcser@x.com", "{\"allow_utf8_local_part\": false}", false},
		{"isEmail", "a@localhost", "{\"require_tld\": false}", true},
		{"isEmail", "te.st@gmail.com", "{\"domain_specific_validation\": true}", false},
		{"isEmail", "test12@gmail.com", "{\"domain_specific_validation\": true}", true},
		{"isEmail", "abc@x.com", "{\"blacklisted_chars\": \"b\"}", false},
		{"isEmail", "a@gmail.com", "{\"host_blacklist\": [\"gmail.com\"]}", false},
		{"isEmail", "a@x.com", "{\"host_whitelist\": [\"gmail.com\"]}", false},
		{"isURL", "foobar.com", "", true},
		{"isURL", "http://www.foobar.com/", "", true},
		{"isURL", "http://127.0.0.1/", "", true},
		{"isURL", "http://[::1]:80", "", true},
		{"isURL", "http://[1080::8:800:200C:417A]/foo", "", true},
		{"isURL", "http://user:pass@www.foobar.com/", "", true},
		{"isURL", "http://:@foobar.com", "", false},
		{"isURL", "http://@foobar.com", "", false},
		{"isURL", "http://www.foobar.com:0/", "", false},
		{"isURL", "http://example.com:0080", "", true},
		{"isURL", "http://www.foobar.com:70000/", "", false},
		{"isURL", "http://foobar/ lol/", "", false},
		{"isURL", "http://exa\u2028mple.com", "", false},
		{"isURL", "mailto:foo@bar.com", "", false},
		{"isURL", "javascript:alert(1)", "", false},
		{"isURL", "javascript:alert(1)@example.com", "", false},
		{"isURL", "user:pass@example.com", "", true},
		{"isURL", "localhost:3000", "", false},
		{"isURL", "//foobar.com", "", false},
		{"isURL", "//foobar.com", "{\"allow_protocol_relative_urls\": true}", true},
		{"isURL", "rtmp://foobar.com", "", false},
		{"isURL", "rtmp://foobar.com", "{\"protocols\": [\"rtmp\"]}", true},
		{"isURL", "foobar.com", "{\"require_protocol\": true}", false},
		{"isURL", "http://localhost/", "{\"require_tld\": false}", true},
		{"isURL", "http://www.foo_bar.com/", "", false},
		{"isURL", "http://www.foo_bar.com/", "{\"allow_underscores\": true}", true},
		{"isURL", "http://\u043a\u0443\u043b\u0456\u043a.\u0443\u043a\u0440", "", true},
		{"isURL", "http://\U0001f600.com", "", true},
		{"isURL", "http://a.com/?q=1", "{\"allow_query_components\": false}", false},
		{"isURL", "http://a.com/#f", "{\"allow_fragments\": false}", false},
		{"isURL", "http://example.com", "{\"host_whitelist\": [\"example.com\"]}", true},
		{"isURL", "http://[::1]", "{\"host_whitelist\": []}", false},
		{"isURL", "http://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.com", "", false},
		{"isUUID", "A987FBC9-4BED-3078-CF07-9141BA07C9F3", "", false},
		{"isUUID", "A987FBC9-4BED-4078-8F07-9141BA07C9F3", "", true},
		{"isUUID", "A987FBC9-4BED-4078-8F07-9141BA07C9F3", "4", true},
		{"isUUID", "A987FBC9-4BED-4078-8F07-9141BA07C9F3", "\"3\"", false},
		{"isUUID", "A987FBC9-4BED-3078-CF07-9141BA07C9F3", "\"loose\"", true},
		{"isUUID", "00000000-0000-0000-0000-000000000000", "", true},
		{"isUUID", "00000000-0000-0000-0000-000000000000", "\"nil\"", true},
		{"isUUID", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF", "\"max\"", true},
		{"isUUID", "a987fbc9-4bed-7078-8f07-9141ba07c9f3", "\"7\"", true},
		{"isUUID", "A987FBC9-4BED-4078-8F07-9141BA07C9F3\n", "", false},
		{"isUUID", "A987FBC9-4BED-4078-8F07-9141BA07C9F3", "\"v4\"", false},
		{"isUUID", "A987FBC9-4BED-4078-8F07-9141BA07C9F3", "9", false},
		{"isISO8601", "2009-05-19", "", true},
		{"isISO8601", "20090519", "", true},
		{"isISO8601", "2009-123", "", true},
		{"isISO8601", "2009-W21-2", "", true},
		{"isISO8601", "2009-05-19 14:39:22-06:00", "", true},
		{"isISO8601", "2010-02-18T16:23:48,444", "", true},
		{"isISO8601", "2007-04-05T24:00", "", true},
		{"isISO8601", "2009-05-19T240030", "", true},
		{"isISO8601", "2009-05-19\n", "", true},
		{"isISO8601", "2009-05-19\u300014:39", "", true},
		{"isISO8601", "2009-05-19 14:39", "{\"strictSeparator\": true}", false},
		{"isISO8601", "2009-05-19T14:39", "{\"strictSeparator\": true}", true},
		{"isISO8601", "2009-0519", "", false},
		{"isISO8601", "2009-02-29", "", true},
		{"isISO8601", "2009-02-29", "{\"strict\": true}", false},
		{"isISO8601", "2008-02-29", "{\"strict\": true}", true},
		{"isISO8601", "2009-366", "{\"strict\": true}", false},
		{"isISO8601", "2020-366", "{\"strict\": true}", true},
		{"isISO8601", "0099-01-01", "{\"strict\": true}", false},
		{"isISO8601", "2021-366\t10", "{\"strict\": true}", false},
		{"isISO8601", "\u0662\u0660\u0660\u0669-05-19", "", false},
		{"isLength", "abc", "{\"min\": 3, \"max\": 3}", true},
		{"isLength", "\U0001f600", "{\"max\": 1}", true},
		{"isLength", "\u2764\ufe0f", "{\"max\": 1}", true},
		{"isLength", "\ufe0f\ufe0f", "{\"max\": 1}", false},
		{"isLength", "\U0001f468\u200d\U0001f469\u200d\U0001f467", "{\"max\": 4}", false},
		{"isLength", "\U0001f468\u200d\U0001f469\u200d\U0001f467", "{\"max\": 5}", true},
		{"isLength", "abc", "{\"min\": 1, \"max\": 6, \"discreteLengths\": [1, 3, 5]}", true},
		{"isLength", "ab", "{\"min\": 1, \"max\": 6, \"discreteLengths\": [1, 3, 5]}", false},
		{"isLength", "", "", true},
		{"isByteLength", "abc", "{\"min\": 3, \"max\": 3}", true},
		{"isByteLength", "\u4e2d", "{\"max\": 2}", false},
		{"isByteLength", "\u4e2d", "{\"min\": 3, \"max\": 3}", true},
		{"isByteLength", "\U0001f600", "{\"min\": 4, \"max\": 4}", true},
		{"isByteLength", "abc", "", false},
		{"isFQDN", "example.com", "", true},
		{"isFQDN", "localhost", "", false},
		{"isFQDN", "localhost", "{\"require_tld\": false}", true},
		{"isFQDN", "example.com.", "{\"allow_trailing_dot\": true}", true},
		{"isFQDN", "*.example.com", "{\"allow_wildcard\": true}", true},
		{"isFQDN", "example.123", "", false},
		{"isFQDN", "example.123", "{\"allow_numeric_tld\": true}", true},
		{"isFQDN", "-a.com", "", false},
		{"isIP", "127.0.0.1", "", true},
		{"isIP", "256.0.0.1", "", false},
		{"isIP", "::1", "", true},
		{"isIP", "fe80::1234%eth0", "", true},
		{"isIP", "::ffff:127.0.0.1", "{\"version\": 6}", true},
		{"isIP", "127.0.0.1", "6", false},
		{"isIP", "127.0.0.1\n", "", false},
	}

	for _, c := range cases {
		t.Run(c.fn+"/"+c.value+"/"+c.options, func(t *testing.T) {
			t.Parallel()

			var opts Value = Undefined{}

			if c.options != "" {
				parsed, err := ParseJSON([]byte(c.options))
				require.NoError(t, err)

				opts = parsed
			}

			got, err := callValidatorJS(t, c.fn, c.value, opts)
			require.NoError(t, err)
			require.Equal(t, c.expected, got)
		})
	}
}

func TestIsUUIDRejectsObjectPrototypeVersion(t *testing.T) {
	t.Parallel()

	_, err := isUUID("A987FBC9-4BED-4078-8F07-9141BA07C9F3", "toString")
	require.ErrorIs(t, err, errUUIDNotRegexp)
}

func TestIsEmailRequiresATopLevelDomain(t *testing.T) {
	t.Parallel()

	require.True(t, IsEmail("root@example.com"))
	require.False(t, IsEmail("admin@localhost"))
	require.False(t, IsEmail("not-an-email"))
}
