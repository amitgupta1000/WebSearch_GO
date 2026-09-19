package models

import "time"

// OrgChart represents the synthesized organizational structure of a company.
type OrgChart struct {
	CompanyName  string       `json:"company_name"`
	Domain       string       `json:"domain,omitempty"`
	Overview     string       `json:"overview"`
	Headquarters string       `json:"headquarters,omitempty"`
	Leadership   []Employee   `json:"leadership"`
	Departments  []Department `json:"departments"`
	Locations    []Location   `json:"locations,omitempty"`
	Sources      []string     `json:"sources"`
	LastUpdated  time.Time    `json:"last_updated"`
}

// Department groups employees by function (Engineering, Sales, etc.).
type Department struct {
	Name        string     `json:"name"`
	Head        string     `json:"head,omitempty"`
	Description string     `json:"description,omitempty"`
	Members     []Employee `json:"members"`
}

// Employee represents a person discovered within an organization.
type Employee struct {
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	Department   string   `json:"department,omitempty"`
	Location     string   `json:"location,omitempty"`
	Bio          string   `json:"bio,omitempty"`
	PublicSource string   `json:"public_source,omitempty"`
	ReportsTo    string   `json:"reports_to,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// Location represents an office or regional operational hub.
type Location struct {
	City         string `json:"city"`
	Country      string `json:"country"`
	Address      string `json:"address,omitempty"`
	HeadcountEst string `json:"headcount_est,omitempty"`
}
