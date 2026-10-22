package alerting

import "github.com/tphakala/birdnet-go/internal/datastore/v2/entities"

// RuleNameIndex records the names and name keys of a set of alert rules, so a
// rule can be checked against the set before it is created.
//
// A rule matches by name, or by a non-empty name key. The key counts because
// the UI shows a keyed rule under the translation of its key, so two rules with
// one key look the same to the user, and the stored name of a keyed rule can be
// a translation that differs from the shipped English name. An empty name key
// never matches: keyless rules are user rules or rows from before name keys
// existed, and treating "" as a key would make them all collide.
type RuleNameIndex struct {
	names    map[string]struct{}
	nameKeys map[string]struct{}
}

// NewRuleNameIndex returns an index holding the names and non-empty name keys
// of rules.
func NewRuleNameIndex(rules []entities.AlertRule) *RuleNameIndex {
	x := &RuleNameIndex{
		names:    make(map[string]struct{}, len(rules)),
		nameKeys: make(map[string]struct{}, len(rules)),
	}
	for i := range rules {
		x.Add(&rules[i])
	}
	return x
}

// Has reports whether the index holds a rule with the name of rule, or with its
// name key when that is not empty.
func (x *RuleNameIndex) Has(rule *entities.AlertRule) bool {
	if _, ok := x.names[rule.Name]; ok {
		return true
	}
	if rule.NameKey == "" {
		return false
	}
	_, ok := x.nameKeys[rule.NameKey]
	return ok
}

// Add records the name of rule, and its name key when that is not empty.
func (x *RuleNameIndex) Add(rule *entities.AlertRule) {
	x.names[rule.Name] = struct{}{}
	if rule.NameKey != "" {
		x.nameKeys[rule.NameKey] = struct{}{}
	}
}
