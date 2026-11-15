package agent

// marketLocalOperationTerms are concrete operating-place phrases. A country
// mention alone (a masthead, a market report, or an extraction country) is not
// enough to call a company local.
var marketLocalOperationTerms = []string{
	"plant", "plants", "factory", "factories", "manufacturing", "manufactures",
	"production", "producer", "refinery", "refining", "blending", "processing",
	"facility", "facilities", "works", "mill", "smelter", "terminal", "terminals",
	"warehouse", "warehouses", "depot", "depots", "office", "offices",
	"operations", "operates", "operating", "distributor", "distributors",
	"dealer", "dealers", "stockist", "stockists", "wholesale", "trading",
}

// marketCountryAliases makes a country filter evidence-based rather than a
// query-string echo. The aliases focus on operating-place language (cities and
// industrial regions), never a generic brand token like "Gulf".
var marketCountryAliases = map[string][]string{
	"india": {
		"india", "indian", "bharat", "mumbai", "pune", "gujarat", "jamnagar",
		"chennai", "delhi", "new delhi", "noida", "gurgaon", "gurugram", "kolkata",
		"ahmedabad", "vadodara", "hyderabad", "bengaluru", "bangalore", "kochi",
		"kandla", "vadinar", "haldia", "paradip", "visakhapatnam", "panipat",
		"mathura", "barauni", "guwahati", "bongaigaon", "cochin", "mangalore",
		"tuticorin", "dahej", "patalganga", "nagothane", "manali", "hazira",
		"surat", "rajkot", "indore", "bhopal", "kanpur", "lucknow", "jaipur",
		"chandigarh", "goa", "assam", "odisha",
	},
	"united states": {
		"united states", "usa", "u s", "us based", "texas", "houston", "louisiana",
		"gulf coast", "california", "new jersey", "ohio", "illinois", "pennsylvania",
		"midwest", "baytown", "port arthur", "corpus christi",
	},
	"uae": {
		"uae", "u a e", "united arab emirates", "dubai", "abu dhabi", "sharjah",
		"jebel ali", "fujairah", "ruwais", "ajman", "ras al khaimah",
	},
	"singapore": {"singapore", "jurong", "tuas", "pulau"},
	"saudi arabia": {
		"saudi arabia", "ksa", "riyadh", "jubail", "yanbu", "dammam", "ras tanura",
	},
	"united kingdom": {
		"united kingdom", "uk", "u k", "britain", "england", "london", "manchester",
		"teesside", "grangemouth", "merseyside", "scotland", "wales",
	},
	"germany": {
		"germany", "deutschland", "hamburg", "ludwigshafen", "frankfurt", "berlin",
		"cologne", "mannheim", "leverkusen", "dormagen",
	},
	"china": {
		"china", "shanghai", "beijing", "ningbo", "guangzhou", "zhejiang", "jiangsu",
		"shandong", "guangdong", "dalian", "tianjin",
	},
	"japan":       {"japan", "tokyo", "osaka", "yokohama", "chiba", "kawasaki", "nagoya"},
	"netherlands": {"netherlands", "holland", "rotterdam", "amsterdam", "terneuzen"},
	"belgium":     {"belgium", "antwerp", "brussels", "zeebrugge"},
	"france":      {"france", "paris", "lyon", "marseille", "le havre", "dunkirk"},
	"south korea": {"south korea", "korea", "seoul", "ulsan", "yeosu", "daesan"},
	"indonesia":   {"indonesia", "jakarta", "surabaya", "batam", "cilegon"},
	"malaysia":    {"malaysia", "kuala lumpur", "johor", "pasir gudang", "kuantan", "bintulu"},
	"thailand":    {"thailand", "bangkok", "rayong", "map ta phut", "laem chabang"},
	"vietnam":     {"vietnam", "ho chi minh", "hanoi", "hai phong", "ba ria vung tau"},
	"australia":   {"australia", "sydney", "melbourne", "perth", "brisbane", "kwinana"},
	"brazil":      {"brazil", "sao paulo", "rio de janeiro", "bahia", "rio grande do sul"},
	"canada":      {"canada", "toronto", "calgary", "alberta", "ontario", "quebec"},
	"mexico":      {"mexico", "mexico city", "monterrey", "veracruz", "tampico"},
	"turkey":      {"turkey", "turkiye", "istanbul", "izmir", "mersin", "gebze"},
}
