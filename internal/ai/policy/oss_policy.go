package policy

import (
	"path"
	"strings"
	"unicode"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// MatchOSSRule matches canonical OSS permission strings shaped "<action> <resource>",
// e.g. "object.read mybucket/logs/app.log" or "bucket.list *".
//
// Both rule and command are split on the FIRST WHITESPACE RUN, not strings.Fields
// like MatchKafkaRule — an object key may itself contain interior whitespace
// (e.g. "object.read mybucket/My Report.pdf" is a valid command; spec decision D4).
//
// The resource half has bucket/key semantics, split on the first '/':
//   - bucket is matched case-sensitively with path.Match.
//   - key: an empty rule key (bare bucket name, or resource ending in "/") matches
//     any key under that prefix at any depth (spec decision D5) — this is what lets
//     "object.read mybucket" and "object.read mybucket/logs/" both act as recursive
//     prefixes instead of only matching a resource equal to the rule string. Otherwise
//     path.Match applies, so '*' does not cross '/'.
//
// action is matched case-insensitively with path.Match; it contains no '/', so
// "object.*" matches both "object.read" and "object.presign.read".
func MatchOSSRule(rule, command string) bool {
	if isWildcardAll(rule) {
		_, _, ok := splitOSSRule(command)
		return ok
	}

	ruleAction, ruleResource, ok := splitOSSRule(rule)
	if !ok {
		return false
	}
	commandAction, commandResource, ok := splitOSSRule(command)
	if !ok {
		return false
	}

	actionMatched, err := path.Match(strings.ToLower(ruleAction), strings.ToLower(commandAction))
	if err != nil {
		logger.Default().Warn("oss policy action match", zap.String("pattern", ruleAction), zap.Error(err))
		return false
	}
	if !actionMatched {
		return false
	}

	return matchOSSResource(ruleResource, commandResource)
}

// splitOSSRule splits value into (action, resource) on the first whitespace run.
// Both halves must be non-empty; ok is false otherwise (e.g. a single token, or an
// empty string).
func splitOSSRule(value string) (action, resource string, ok bool) {
	trimmed := strings.TrimSpace(value)
	idx := strings.IndexFunc(trimmed, unicode.IsSpace)
	if idx <= 0 {
		return "", "", false
	}
	resource = strings.TrimSpace(trimmed[idx:])
	if resource == "" {
		return "", "", false
	}
	return trimmed[:idx], resource, true
}

// matchOSSResource matches a "<bucket>/<key>" rule pattern against a value of the
// same shape. bucket is path.Match'd as a whole segment; an empty rule key (bare
// bucket, or a resource ending in "/") matches any key under that prefix at any
// depth, otherwise the key is path.Match'd with '*' not crossing '/'.
func matchOSSResource(rulePattern, value string) bool {
	ruleBucket, ruleKey := splitOSSResource(rulePattern)
	valueBucket, valueKey := splitOSSResource(value)

	bucketMatched, err := path.Match(ruleBucket, valueBucket)
	if err != nil {
		logger.Default().Warn("oss policy bucket match", zap.String("pattern", ruleBucket), zap.Error(err))
		return false
	}
	if !bucketMatched {
		return false
	}

	if ruleKey == "" {
		return true
	}
	if strings.HasSuffix(ruleKey, "/") {
		return strings.HasPrefix(valueKey, ruleKey)
	}

	keyMatched, err := path.Match(ruleKey, valueKey)
	if err != nil {
		logger.Default().Warn("oss policy key match", zap.String("pattern", ruleKey), zap.Error(err))
		return false
	}
	return keyMatched
}

// splitOSSResource splits a "<bucket>/<key>" resource on the first '/'. A resource
// with no '/' at all (a bare bucket name, or the literal "*" used by bucket.list) is
// treated as bucket-only with an empty key.
func splitOSSResource(resource string) (bucket, key string) {
	idx := strings.IndexByte(resource, '/')
	if idx < 0 {
		return resource, ""
	}
	return resource[:idx], resource[idx+1:]
}
