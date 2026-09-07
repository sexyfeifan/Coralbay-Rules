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
	Name          string               `json:"name"`
	Sources       []string             `json:"sources"`
	Clients       []string             `json:"clients"`
	Rules         []routingRuleChoice  `json:"rules"`
	Global        routingFilter        `json:"global"`
	Strategy      string               `json:"strategy"`
	Match         string               `json:"match"` // proxy or direct
	IntervalHours int                  `json:"interval_hours"`
	RuleDelivery  *routingRuleDelivery `json:"rule_delivery,omitempty"`
}

// A missing field identifies a pre-provider profile and retains inline output.
// New clients explicitly request provider/local; validation never migrates old
// profiles merely because they were opened or refreshed.
type routingRuleDelivery struct {
	Mode   string `json:"mode"`
	Source string `json:"source,omitempty"`
}

type routingProviderPreview struct {
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	Behavior  string `json:"behavior"`
	Format    string `json:"format"`
	URL       string `json:"url"`
	SourceURL string `json:"source_url"`
	LocalURL  string `json:"local_url"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	Count     int    `json:"count"`
}

type routingBuildMetadata struct {
	RuleDelivery         routingRuleDelivery      `json:"rule_delivery"`
	RuleLibrary          string                   `json:"rule_library"`
	GeneratedAt          string                   `json:"generated_at"`
	RuleResources        []routingProviderPreview `json:"rule_resources"`
	ExternalDependencies []string                 `json:"external_dependencies"`
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
	routingBuildMetadata
	Outputs     map[string]string     `json:"outputs"`
	NodeCount   int                   `json:"node_count"`
	Groups      []routingGroupPreview `json:"groups"`
	Nodes       []routingNodePreview  `json:"nodes"`
	Revision    string                `json:"rule_revision"`
	RuleOrder   []string              `json:"rule_order"`
	Warnings    []string              `json:"warnings"`
	UsageHeader string                `json:"-"`
}
