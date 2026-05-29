// Target parsing for unified locator syntax.
// A target is a strategy:value string that identifies a UI element across platforms.
package action

import "strings"

// Strategy identifies how to locate a UI element.
type Strategy string

const (
	StrategyCSS   Strategy = "css"
	StrategyXPath Strategy = "xpath"
	StrategyText  Strategy = "text"
	StrategyID    Strategy = "id"
	StrategyAcc   Strategy = "acc"
	StrategyXY    Strategy = "xy"
)

// Target is a parsed locator with a strategy and value.
type Target struct {
	Strategy Strategy
	Value    string
}

// ParseTarget splits a "strategy:value" string into its components.
// If no strategy prefix is found, defaultStrategy is used.
func ParseTarget(raw string, defaultStrategy Strategy) Target {
	for _, s := range []Strategy{StrategyCSS, StrategyXPath, StrategyText, StrategyID, StrategyAcc, StrategyXY} {
		prefix := string(s) + ":"
		if strings.HasPrefix(raw, prefix) {
			return Target{Strategy: s, Value: raw[len(prefix):]}
		}
	}
	return Target{Strategy: defaultStrategy, Value: raw}
}
