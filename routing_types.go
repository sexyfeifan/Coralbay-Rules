package main

// These types belong only to custom routing subscriptions. Legacy subscription
// parameters and the 666OS publication pipeline deliberately do not use them.
type routingFilter struct {
	Regions        []string `json:"regions"`
	ExcludeRegions []string `json:"exclude_regions"`
	Include        string   `json:"include"`
	Exclude        string   `json:"exclude"`
}

type routingRuleChoice struct {
	ID       string         `json:"id"`
	Action   string         `json:"action"`             // proxy, direct, reject
	Filter   *routingFilter `json:"filter,omitempty"`   // nil inherits global
	Strategy string         `json:"strategy,omitempty"` // select, url-test, fallback
}

type routingProfileSpec struct {
	Name          string              `json:"name"`
	Sources       []string            `json:"sources"`
	Clients       []string            `json:"clients"`
	Rules         []routingRuleChoice `json:"rules"`
	Global        routingFilter       `json:"global"`
	Strategy      string              `json:"strategy"`
	Match         string              `json:"match"` // proxy or direct
	IntervalHours int                 `json:"interval_hours"`
}

type routingNodePreview struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Region   string `json:"region"`
	Excluded string `json:"excluded,omitempty"`
}

type routingGroupPreview struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Strategy string   `json:"strategy"`
	Nodes    []string `json:"nodes"`
}

type routingBuildResult struct {
	Outputs     map[string]string     `json:"outputs"`
	NodeCount   int                   `json:"node_count"`
	Groups      []routingGroupPreview `json:"groups"`
	Nodes       []routingNodePreview  `json:"nodes"`
	Revision    string                `json:"rule_revision"`
	RuleOrder   []string              `json:"rule_order"`
	Warnings    []string              `json:"warnings"`
	UsageHeader string                `json:"-"`
}
