package admin

// IPLocation is display metadata, never an identity or authorization fact.
type IPLocation struct {
	Status   string `json:"status"`
	Country  string `json:"country,omitempty"`
	Province string `json:"province,omitempty"`
	City     string `json:"city,omitempty"`
	Region   string `json:"region,omitempty"`
	ISP      string `json:"isp,omitempty"`
}

type IPLocationLookup interface{ Lookup(string) IPLocation }
