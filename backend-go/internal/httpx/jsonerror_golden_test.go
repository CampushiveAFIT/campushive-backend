package httpx

import "testing"

var jsonErrorGolden = []struct {
	source  string
	message string
}{
	{"{\"email\":", "Unexpected end of JSON input"},
	{"not json", "Unexpected token 'n', \"not json\" is not valid JSON"},
	{"{\"a\":1,}", "Expected double-quoted property name in JSON at position 7 (line 1 column 8)"},
	{"{,}", "Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
	{"{\"a\" 1}", "Expected ':' after property name in JSON at position 5 (line 1 column 6)"},
	{"[1,2", "Expected ',' or ']' after array element in JSON at position 4 (line 1 column 5)"},
	{"tru", "Unexpected token 't', \"tru\" is not valid JSON"},
	{"   ", "Unexpected end of JSON input"},
	{"{\"a\":1} trailing", "Unexpected non-whitespace character after JSON at position 8 (line 1 column 9)"},
	{"nullx", "Unexpected token 'n', \"nullx\" is not valid JSON"},
	{"{\"a\":\"\\q\"}", "Bad escaped character in JSON at position 7 (line 1 column 8)"},
	{"{\"a\":01}", "Unexpected number in JSON at position 6 (line 1 column 7)"},
	{"{\"a\":undefined}", "Unexpected token 'u', \"{\"a\":undefined}\" is not valid JSON"},
	{"abcdefghij", "Unexpected token 'a', \"abcdefghij\" is not valid JSON"},
	{"abcdefghijklmnopq", "Unexpected token 'a', \"abcdefghijklmnopq\" is not valid JSON"},
	{"averyveryverylongtokenhere_that_is_long_n", "Unexpected token 'a', \"averyveryv\"... is not valid JSON"},
	{"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxn", "Unexpected token 'x', \"xxxxxxxxxx\"... is not valid JSON"},
	{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Unexpected token 'a', \"aaaaaaaaaa\"... is not valid JSON"},
	{"{\"a\":1}   x", "Unexpected non-whitespace character after JSON at position 10 (line 1 column 11)"},
	{"{\"a\":1}xyz", "Unexpected non-whitespace character after JSON at position 7 (line 1 column 8)"},
	{"{\"a\":1}}", "Unexpected non-whitespace character after JSON at position 7 (line 1 column 8)"},
	{"{\"a\":[1,2}", "Expected ',' or ']' after array element in JSON at position 9 (line 1 column 10)"},
	{"{\"a\":{\"b\":1}", "Expected ',' or '}' after property value in JSON at position 12 (line 1 column 13)"},
	{"{\"a\":tru}", "Unexpected token '}', \"{\"a\":tru}\" is not valid JSON"},
	{"{\"a\":}", "Unexpected token '}', \"{\"a\":}\" is not valid JSON"},
	{"{\"a\":\"unterminated", "Unterminated string in JSON at position 18 (line 1 column 19)"},
	{"{\"a\":\"\\u12\"}", "Bad Unicode escape in JSON at position 10 (line 1 column 11)"},
	{"{\"a\":\"tab\there\"}", "Bad control character in string literal in JSON at position 9 (line 1 column 10)"},
	{"{\"a\":\"ok\"}extra", "Unexpected non-whitespace character after JSON at position 10 (line 1 column 11)"},
	{"[", "Unexpected end of JSON input"},
	{"]", "Unexpected token ']', \"]\" is not valid JSON"},
	{"{", "Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
	{"}", "Unexpected token '}', \"}\" is not valid JSON"},
	{"{\"a\":-}", "No number after minus sign in JSON at position 6 (line 1 column 7)"},
	{"{\"a\":1e}", "Exponent part is missing a number in JSON at position 7 (line 1 column 8)"},
	{"{\"a\":.5}", "Unexpected token '.', \"{\"a\":.5}\" is not valid JSON"},
	{"{\"a\":+1}", "Unexpected token '+', \"{\"a\":+1}\" is not valid JSON"},
	{"{\"a\":1.}", "Unterminated fractional number in JSON at position 7 (line 1 column 8)"},
	{"{\"a\":Infinity}", "Unexpected token 'I', \"{\"a\":Infinity}\" is not valid JSON"},
	{"{\"a\":NaN}", "Unexpected token 'N', \"{\"a\":NaN}\" is not valid JSON"},
	{"{\"a\":\"\\x41\"}", "Bad escaped character in JSON at position 7 (line 1 column 8)"},
	{"{\"a\":\"\\\"}", "Unterminated string in JSON at position 9 (line 1 column 10)"},
	{"\t\n {\"a\":1,} \n", "Expected double-quoted property name in JSON at position 10 (line 2 column 9)"},
	{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaX", "Unexpected token 'a', \"aaaaaaaaaa\"... is not valid JSON"},
	{"Xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Unexpected token 'X', \"Xaaaaaaaaa\"... is not valid JSON"},
	{"aaaaaaaaaaaaaaaaaaaaaaXaaaaaaaaaaaaaaaaaaaaaa", "Unexpected token 'a', \"aaaaaaaaaa\"... is not valid JSON"},
	{"{\"email\":\"a@b.com\",\"password\":\"Abcdefg1\",}", "Expected double-quoted property name in JSON at position 41 (line 1 column 42)"},
	{"{\"email\":\"a@b.com\" \"password\":\"x\"}", "Expected ',' or '}' after property value in JSON at position 19 (line 1 column 20)"},
	{"[{\"a\":1},{,}]", "Expected property name or '}' in JSON at position 10 (line 1 column 11)"},
	{"\"unterminated", "Unexpected token '\"', \"\"unterminated\" is not valid JSON"},
	{"\"ok\"x", "Unexpected token '\"', \"\"ok\"x\" is not valid JSON"},
	{"12x", "Unexpected token '1', \"12x\" is not valid JSON"},
	{"12.5x", "Unexpected token '1', \"12.5x\" is not valid JSON"},
	{"-x", "Unexpected token '-', \"-x\" is not valid JSON"},
	{"+x", "Unexpected token '+', \"+x\" is not valid JSON"},
	{".x", "Unexpected token '.', \".x\" is not valid JSON"},
	{"*", "Unexpected token '*', \"*\" is not valid JSON"},
	{"@", "Unexpected token '@', \"@\" is not valid JSON"},
	{"#", "Unexpected token '#', \"#\" is not valid JSON"},
	{"null true", "Unexpected token 'n', \"null true\" is not valid JSON"},
	{"true false", "Unexpected token 't', \"true false\" is not valid JSON"},
	{"1 2", "Unexpected token '1', \"1 2\" is not valid JSON"},
	{"-", "Unexpected token '-', \"-\" is not valid JSON"},
	{"\"a\nb\"", "Unexpected token '\"', \"\"a\nb\"\" is not valid JSON"},
}

func TestScanJSONMatchesV8(t *testing.T) {
	for _, tc := range jsonErrorGolden {
		err := scanJSON(tc.source)
		if err == nil {
			t.Errorf("%q: want error %q", tc.source, tc.message)
			continue
		}
		if err.message != tc.message {
			t.Errorf("%q:\n got %q\nwant %q", tc.source, err.message, tc.message)
		}
	}
}
