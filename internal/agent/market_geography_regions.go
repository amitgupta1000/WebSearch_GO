package agent

// marketRegionTerms is intentionally a small operating vocabulary, not a
// political geography database. It lets a region-scoped search distinguish a
// regional operator from a global-only corporate page.
var marketRegionTerms = map[string][]string{
	"asia": {
		"asia", "asia pacific", "apac", "india", "china", "japan", "south korea",
		"singapore", "indonesia", "malaysia", "thailand", "vietnam", "philippines",
		"uae", "saudi arabia", "qatar", "oman", "kuwait", "bahrain", "pakistan",
		"bangladesh", "sri lanka",
	},
	"asia pacific": {
		"asia pacific", "apac", "asia", "india", "china", "japan", "south korea",
		"singapore", "indonesia", "malaysia", "thailand", "vietnam", "philippines",
		"australia", "new zealand",
	},
	"apac": {
		"apac", "asia pacific", "asia", "india", "china", "japan", "south korea",
		"singapore", "indonesia", "malaysia", "thailand", "vietnam", "philippines",
		"australia", "new zealand",
	},
	"southeast asia": {
		"southeast asia", "south east asia", "asean", "singapore", "indonesia",
		"malaysia", "thailand", "vietnam", "philippines", "brunei", "cambodia",
		"laos", "myanmar",
	},
	"south asia": {"south asia", "india", "pakistan", "bangladesh", "sri lanka", "nepal", "bhutan"},
	"middle east": {
		"middle east", "gcc", "uae", "saudi arabia", "qatar", "oman", "kuwait",
		"bahrain", "turkey", "israel", "jordan", "lebanon", "iraq", "iran",
	},
	"gcc": {"gcc", "uae", "saudi arabia", "qatar", "oman", "kuwait", "bahrain"},
	"africa": {
		"africa", "south africa", "nigeria", "egypt", "kenya", "morocco", "algeria",
		"tunisia", "ghana", "ethiopia", "tanzania",
	},
	"mea": {
		"mea", "middle east", "africa", "uae", "saudi arabia", "qatar", "oman",
		"kuwait", "bahrain", "south africa", "nigeria", "egypt", "kenya", "morocco",
	},
	"europe": {
		"europe", "germany", "france", "united kingdom", "uk", "netherlands",
		"belgium", "spain", "italy", "poland", "sweden", "norway", "denmark",
		"finland", "switzerland", "austria", "ireland", "portugal", "greece",
		"turkey", "russia",
	},
	"north america": {"north america", "united states", "usa", "canada", "mexico"},
	"south america": {
		"south america", "brazil", "argentina", "chile", "colombia", "peru",
		"venezuela", "ecuador", "bolivia", "uruguay", "paraguay",
	},
	"emea": {
		"emea", "europe", "middle east", "africa", "germany", "france",
		"united kingdom", "uae", "saudi arabia", "south africa", "nigeria", "kenya",
	},
}
