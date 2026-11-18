// Command ontology drives the chemical value-chain review roundtrip:
//
//	go run ./cmd/ontology lint   -file internal/ontology/data/chemicals.json
//	go run ./cmd/ontology emit   -file internal/ontology/data/chemicals.json -out internal/ontology/data/chemicals.json
//	go run ./cmd/ontology review -file internal/ontology/data/chemicals.json -out internal/ontology/data/REVIEW.md
//	go run ./cmd/ontology merge  -base data/chemicals.json -patch curated.json -out data/chemicals.json
//	go run ./cmd/ontology diff   -before data/chemicals.json -after curated.json
//	go run ./cmd/ontology harvest -out data/harvest.json
//
// Nothing here invents chemistry: the commands validate, serialise and merge the
// JSON document a domain expert curates.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

const usage = `usage: ontology <command> [flags]

  lint    -file PATH                     validate the document; exit 2 on errors
  emit    [-file PATH] [-out PATH]       write the canonical, deterministic JSON
  review  [-file PATH] [-out PATH]       write the domain reviewer's worksheet
  approve -file PATH -by NAME [-out PATH]
                                         mark every proposed node approved
  merge   -base PATH -patch PATH [-out PATH]
                                         fold a curated file/patch onto the base
  diff    -before PATH -after PATH       list added/removed/changed nodes
  harvest [-file PATH] [-out PATH]       trade DB canonical names as proposed substances
  explain -activity TEXT [-mode seller|buyer] [-geography TEXT] [-file PATH] [-json]
                                         resolve an activity's research path without a run
  graph   [-activity TEXT | -seed ID] [-hops N] [-kind LIST] [-edge LIST] [-out PATH] [-json]
                                         draw the knowledge graph (HTML page, SVG or JSON)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "lint":
		err = runLint(os.Args[2:])
	case "emit":
		err = runEmit(os.Args[2:])
	case "review":
		err = runReview(os.Args[2:])
	case "approve":
		err = runApprove(os.Args[2:])
	case "merge":
		err = runMerge(os.Args[2:])
	case "diff":
		err = runDiff(os.Args[2:])
	case "harvest":
		err = runHarvest(os.Args[2:])
	case "explain":
		err = runExplain(os.Args[2:])
	case "graph":
		err = runGraph(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func load(path string) (*ontology.Ontology, error) {
	if strings.TrimSpace(path) == "" {
		return ontology.Seed()
	}
	return ontology.LoadFile(path)
}

func write(content []byte, out string) error {
	if strings.TrimSpace(out) == "" {
		_, err := os.Stdout.Write(content)
		return err
	}
	return os.WriteFile(out, content, 0o644)
}

func runLint(args []string) error {
	set := flag.NewFlagSet("lint", flag.ExitOnError)
	file := set.String("file", "", "document to lint (default: embedded seed)")
	out := set.String("out", "", "write the markdown lint report here instead of the console summary")
	_ = set.Parse(args)
	document, err := load(*file)
	if err != nil {
		return err
	}
	issues := ontology.SortedIssues(ontology.Lint(document))
	errors := 0
	for _, issue := range issues {
		if issue.Severity == ontology.SeverityError {
			errors++
		}
	}
	if *out != "" {
		if err := write([]byte(ontology.IssuesMarkdown(document, issues)), *out); err != nil {
			return err
		}
		fmt.Printf("%d node(s), %d issue(s), %d error(s) → %s\n", len(document.Nodes), len(issues), errors, *out)
		if errors > 0 {
			os.Exit(2)
		}
		return nil
	}
	for _, issue := range issues {
		fmt.Println(issue.String())
	}
	fmt.Printf("%d node(s), %d issue(s), %d error(s)\n", len(document.Nodes), len(issues), errors)
	if errors > 0 {
		os.Exit(2)
	}
	return nil
}

func runEmit(args []string) error {
	set := flag.NewFlagSet("emit", flag.ExitOnError)
	file := set.String("file", "", "document to read (default: embedded seed)")
	out := set.String("out", "", "write to this path instead of stdout")
	_ = set.Parse(args)
	document, err := load(*file)
	if err != nil {
		return err
	}
	raw, err := ontology.Marshal(document)
	if err != nil {
		return err
	}
	return write(raw, *out)
}

func runReview(args []string) error {
	set := flag.NewFlagSet("review", flag.ExitOnError)
	file := set.String("file", "", "document to review (default: embedded seed)")
	out := set.String("out", "", "write to this path instead of stdout")
	_ = set.Parse(args)
	document, err := load(*file)
	if err != nil {
		return err
	}
	return write([]byte(ontology.ReviewMarkdown(document)), *out)
}

// runApprove marks every proposed node approved and records the reviewer, then
// writes the document (the operator's sign-off, kept reproducible).
func runApprove(args []string) error {
	set := flag.NewFlagSet("approve", flag.ExitOnError)
	file := set.String("file", "", "document to approve")
	by := set.String("by", "", "who is approving it (recorded as reviewed_by)")
	out := set.String("out", "", "write the approved document here (default: in place when -file is set, else stdout)")
	_ = set.Parse(args)
	if *file == "" {
		return fmt.Errorf("approve needs -file")
	}
	if strings.TrimSpace(*by) == "" {
		return fmt.Errorf("approve needs -by: an approval without a reviewer name is not an audit trail")
	}
	document, err := ontology.LoadFileRaw(*file)
	if err != nil {
		return err
	}
	approved := ontology.Approve(document, *by)
	notes := strings.TrimSpace(document.Notes)
	if notes != "" {
		notes += " "
	}
	document.Notes = notes + "Review accepted by " + strings.TrimSpace(*by) + "; the runtime gate resolves only approved nodes."
	raw, err := ontology.Marshal(document)
	if err != nil {
		return err
	}
	target := *out
	if target == "" {
		target = *file
	}
	if err := write(raw, target); err != nil {
		return err
	}
	fmt.Printf("approved %d node(s) for %q → %s\n", approved, *by, target)
	for _, issue := range ontology.SortedIssues(ontology.Lint(document)) {
		if issue.Severity == ontology.SeverityError {
			fmt.Fprintln(os.Stderr, "lint:", issue.String())
		}
	}
	return nil
}

func runMerge(args []string) error {
	set := flag.NewFlagSet("merge", flag.ExitOnError)
	basePath := set.String("base", "", "reviewed base document")
	patchPath := set.String("patch", "", "curated file or corrections-only patch")
	out := set.String("out", "", "write the merged document here (default: stdout)")
	_ = set.Parse(args)
	if *basePath == "" || *patchPath == "" {
		return fmt.Errorf("merge needs -base and -patch")
	}
	base, err := ontology.LoadFile(*basePath)
	if err != nil {
		return err
	}
	patch, err := ontology.LoadFileRaw(*patchPath)
	if err != nil {
		return err
	}
	merged, err := ontology.Merge(base, patch)
	if err != nil {
		return err
	}
	raw, err := ontology.Marshal(merged)
	if err != nil {
		return err
	}
	if err := write(raw, *out); err != nil {
		return err
	}
	for _, issue := range ontology.SortedIssues(ontology.Lint(merged)) {
		if issue.Severity == ontology.SeverityError {
			fmt.Fprintln(os.Stderr, "lint:", issue.String())
		}
	}
	return nil
}

func runDiff(args []string) error {
	set := flag.NewFlagSet("diff", flag.ExitOnError)
	beforePath := set.String("before", "", "document that was submitted")
	afterPath := set.String("after", "", "document that came back")
	_ = set.Parse(args)
	if *beforePath == "" || *afterPath == "" {
		return fmt.Errorf("diff needs -before and -after")
	}
	before, err := ontology.LoadFile(*beforePath)
	if err != nil {
		return err
	}
	after, err := ontology.LoadFileRaw(*afterPath)
	if err != nil {
		return err
	}
	// A corrections-only return is comparable only after it is applied to the
	// submitted document; a full-file return is already the edited document.
	if len(after.Corrections) > 0 {
		merged, err := ontology.Merge(before, after)
		if err != nil {
			return err
		}
		after = merged
	}
	fmt.Print(ontology.Diff(before, after))
	return nil
}

func runHarvest(args []string) error {
	set := flag.NewFlagSet("harvest", flag.ExitOnError)
	file := set.String("file", "", "base document to reconcile against (default: embedded seed)")
	out := set.String("out", "", "write the corrections document here (default: stdout)")
	_ = set.Parse(args)
	base, err := load(*file)
	if err != nil {
		return err
	}
	cfg := config.Load()
	if strings.TrimSpace(cfg.TradeDBAPIURL) == "" {
		return fmt.Errorf("TRADE_DB_API_URL is not set, so the canonical product vocabulary cannot be harvested")
	}
	client := tools.NewTradeDBClient(cfg.TradeDBAPIURL, cfg.TradeDBTimeout, cfg.TradeDBConcurrency)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	options, err := client.FilterOptions(ctx)
	if err != nil {
		return fmt.Errorf("trade DB filters: %w", err)
	}
	corrections, skipped := ontology.Harvest(base, options.CanonicalProductNames)
	document := ontology.HarvestDocument("", corrections)
	raw, err := ontology.Marshal(document)
	if err != nil {
		return err
	}
	if err := write(raw, *out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "harvested %d new substance(s); %d name(s) already covered\n", len(corrections), len(skipped))
	return nil
}

// runGraph draws the knowledge graph as a self-contained page, an SVG or JSON. The
// file is the same document the console serves, so a saved graph and a link agree.
func runGraph(args []string) error {
	set := flag.NewFlagSet("graph", flag.ExitOnError)
	seed := set.String("seed", "", "node id or label to centre on (default: the whole graph)")
	activity := set.String("activity", "", "alias of -seed, for an operator's activity")
	hops := set.Int("hops", 0, "neighbourhood radius around the seed (default 2)")
	kinds := set.String("kind", "", "comma-separated kinds to keep: substance, category, process, end-product")
	edges := set.String("edge", "", "comma-separated edge families to keep (default: all)")
	file := set.String("file", "", "document to draw (default: embedded seed)")
	out := set.String("out", "", "write here instead of stdout; .json and .svg choose the format")
	asJSON := set.Bool("json", false, "print the view as JSON")
	title := set.String("title", "", "page title")
	zoom := set.Int("zoom", 0, "page zoom percent: 100 fits the container, larger scrolls (0 means 100)")
	labels := set.Bool("labels", false, "label every node instead of rings 0-1 and hubs")
	_ = set.Parse(args)
	if strings.TrimSpace(*seed) == "" {
		*seed = *activity
	}
	if strings.TrimSpace(*seed) == "" && len(set.Args()) > 0 {
		*seed = strings.Join(set.Args(), " ")
	}
	document, err := load(*file)
	if err != nil {
		return err
	}
	view := document.View(ontology.ViewOptions{
		Seed:  *seed,
		Hops:  *hops,
		Kinds: graphKinds(splitList(*kinds)),
		Edges: splitList(*edges),
	})

	target := strings.ToLower(strings.TrimSpace(*out))
	switch {
	case *asJSON || strings.HasSuffix(target, ".json"):
		raw, err := ontology.MarshalView(view)
		if err != nil {
			return err
		}
		if err := write(raw, *out); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%d node(s), %d edge(s) → %s\n", len(view.Nodes), len(view.Edges), targetOrStdout(*out))
	case strings.HasSuffix(target, ".svg"):
		svg := ontology.RenderSVG(view, ontology.SVGOptions{LabelAll: *labels || len(view.Nodes) <= 60})
		if err := write([]byte(svg), *out); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%d node(s), %d edge(s) → %s\n", len(view.Nodes), len(view.Edges), *out)
	default:
		page := ontology.RenderPage(view, ontology.PageOptions{Title: *title, ZoomPercent: *zoom, LabelAll: *labels})
		if err := write(page, *out); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%d node(s), %d edge(s) → %s\n", len(view.Nodes), len(view.Edges), targetOrStdout(*out))
	}
	return nil
}

// splitList parses a comma-separated flag value.
func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// graphKinds converts the -kind flag values to node kinds.
func graphKinds(names []string) []ontology.Kind {
	kinds := make([]ontology.Kind, 0, len(names))
	for _, name := range names {
		kinds = append(kinds, ontology.Kind(name))
	}
	return kinds
}

func targetOrStdout(out string) string {
	if strings.TrimSpace(out) == "" {
		return "stdout"
	}
	return out
}

// runExplain prints the research path an activity takes: the same resolution the
// Seller and Buyer Landscape modes run at their start, minus the run itself. It is
// deterministic by default (no model call), which is what curation reviews.
func runExplain(args []string) error {
	set := flag.NewFlagSet("explain", flag.ExitOnError)
	activity := set.String("activity", "", "the operator's activity, e.g. \"oil blending\"")
	geography := set.String("geography", "", "geography the run targets")
	mode := set.String("mode", "seller", "seller (who makes it) or buyer (who consumes it)")
	file := set.String("file", "", "document to resolve against (default: embedded seed)")
	asJSON := set.Bool("json", false, "print the resolution as JSON")
	_ = set.Parse(args)
	if strings.TrimSpace(*activity) == "" {
		*activity = strings.Join(set.Args(), " ")
	}
	if strings.TrimSpace(*activity) == "" {
		return fmt.Errorf("explain needs an activity, e.g. -activity \"oil blending\"")
	}
	document, err := load(*file)
	if err != nil {
		return err
	}
	resolution, err := agent.ResolveActivityAgainst(context.Background(), nil, document, agent.ActivityQuery{
		Activity: *activity, Geography: *geography, Mode: agent.ActivityMode(*mode),
	})
	if err != nil {
		return err
	}
	if *asJSON {
		raw, err := json.MarshalIndent(resolution, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(raw))
		return nil
	}
	fmt.Printf("activity: %s\nmode: %s\npath: %s\n", resolution.Activity, resolution.Mode, resolution.Path)
	if resolution.NodeID != "" {
		fmt.Printf("node: %s (%s)\n", resolution.NodeLabel, resolution.NodeID)
	}
	if resolution.Category != "" {
		fmt.Printf("category: %s [%s]\n", resolution.Category, resolution.CategorySource)
		if resolution.CategoryRationale != "" {
			fmt.Printf("why: %s\n", resolution.CategoryRationale)
		}
	}
	for _, family := range resolution.Families {
		fmt.Printf("family %d: %s (%s, %s)\n", family.Rank+1, family.Label, family.ID, family.Reason)
	}
	if len(resolution.SearchTerms) > 0 {
		fmt.Printf("search terms: %s\n", strings.Join(resolution.SearchTerms, " | "))
	}
	if len(resolution.CategoryLanes) > 0 {
		fmt.Printf("category lanes: %s\n", strings.Join(resolution.CategoryLanes, " | "))
	}
	for _, stage := range resolution.Stages {
		fmt.Printf("stage %d: %s (operators: %s; products: %s)\n",
			stage.Distance, stage.Name, strings.Join(stage.Operators, ", "), strings.Join(stage.Products, ", "))
	}
	for _, warning := range resolution.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
	if resolution.Path == agent.ActivityPathUnmodelled {
		fmt.Println("note: the model will plan this path; add the activity to the ontology to make it graph-guided.")
	}
	return nil
}

