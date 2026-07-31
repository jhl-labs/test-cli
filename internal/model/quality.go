package model

import "time"

// QualityReport is the evidence-based QA assessment derived from normalized
// test results, coverage, and a lightweight scan of test source files. It is
// intentionally made of portable metrics rather than ecosystem-specific
// framework concepts.
type QualityReport struct {
	Score            int                `json:"score"`
	Grade            string             `json:"grade"`
	Risk             string             `json:"risk"`
	Dimensions       []QualityDimension `json:"dimensions"`
	Summary          FindingSummary     `json:"summary"`
	Findings         []QualityFinding   `json:"findings"`
	Static           StaticAnalysis     `json:"static"`
	SlowTests        []TestHotspot      `json:"slowTests,omitempty"`
	CoverageHotspots []CoverageHotspot  `json:"coverageHotspots,omitempty"`
	Comparison       *QualityComparison `json:"comparison,omitempty"`
	History          *QualityHistory    `json:"history,omitempty"`
	Changes          *ChangeAnalysis    `json:"changes,omitempty"`
}

// QualityDimension explains one component of the overall weighted score.
type QualityDimension struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Score  int    `json:"score"`
	Weight int    `json:"weight"`
	Detail string `json:"detail"`
}

// FindingSummary holds severity rollups for quality findings.
type FindingSummary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Info     int `json:"info"`
}

// QualityFinding is an actionable, evidence-backed diagnostic. ID is stable so
// CI and agents can suppress or route individual rule families.
type QualityFinding struct {
	ID             string `json:"id"`
	Severity       string `json:"severity"`
	Category       string `json:"category"`
	Title          string `json:"title"`
	Detail         string `json:"detail"`
	Evidence       string `json:"evidence,omitempty"`
	Recommendation string `json:"recommendation"`
}

// StaticAnalysis summarizes the repository's production/test code inventory
// and portable test-smell rules found without executing code.
type StaticAnalysis struct {
	SourceFiles       int                 `json:"sourceFiles"`
	TestFiles         int                 `json:"testFiles"`
	SourceLines       int                 `json:"sourceLines"`
	TestLines         int                 `json:"testLines"`
	TestToSourceRatio float64             `json:"testToSourceRatio"`
	Inventory         []LanguageInventory `json:"inventory,omitempty"`
	Smells            []StaticSmell       `json:"smells,omitempty"`
	TestMappings      []TestMapping       `json:"testMappings,omitempty"`
	UnmappedSources   int                 `json:"unmappedSources"`
}

// RiskAnalysis ranks source files by combined regression risk derived from git
// churn, a complexity approximation, and the coverage gap. It is omitted when
// the project root is not a usable git repository.
type RiskAnalysis struct {
	Base  string     `json:"base"`
	Files []RiskFile `json:"files,omitempty"`
}

// RiskFile is one ranked entry of the risk analysis. RiskScore is a normalized
// 0..1 product of within-project churn and complexity percentiles and the
// uncovered fraction, so any near-zero axis suppresses the score.
type RiskFile struct {
	Path        string  `json:"path"`
	Language    string  `json:"language,omitempty"`
	Churn       int     `json:"churn"`
	Complexity  int     `json:"complexity"`
	CoveragePct float64 `json:"coveragePct"`
	RiskScore   float64 `json:"riskScore"`
}

// TestMapping links one production source file to the test files that appear
// to exercise it. The mapping is name/import-heuristic based; Heuristic is
// always true so consumers do not over-trust it.
type TestMapping struct {
	SourcePath     string   `json:"sourcePath"`
	Language       string   `json:"language,omitempty"`
	TestPaths      []string `json:"testPaths,omitempty"`
	AssertionCount int      `json:"assertionCount"`
	TestFuncCount  int      `json:"testFuncCount"`
	Heuristic      bool     `json:"heuristic"`
}

// LanguageInventory is a per-language source/test code inventory.
type LanguageInventory struct {
	Language    string `json:"language"`
	SourceFiles int    `json:"sourceFiles"`
	TestFiles   int    `json:"testFiles"`
	SourceLines int    `json:"sourceLines"`
	TestLines   int    `json:"testLines"`
}

// StaticSmell identifies one heuristic test-code issue and its exact location.
type StaticSmell struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
}

// TestHotspot is a slow test ranked from the current run. It is not labelled
// flaky because flakiness requires evidence from multiple runs.
type TestHotspot struct {
	Language   string  `json:"language,omitempty"`
	Suite      string  `json:"suite"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	DurationMs float64 `json:"durationMs"`
}

// CoverageHotspot ranks files by uncovered executable lines.
type CoverageHotspot struct {
	Path           string  `json:"path"`
	Language       string  `json:"language,omitempty"`
	CoveragePct    float64 `json:"coveragePct"`
	UncoveredLines int     `json:"uncoveredLines"`
	Risk           string  `json:"risk"`
}

// QualityComparison captures regressions and improvements against an explicit
// baseline report. Flakiness is still not inferred from two points; repeated
// observations are required for that classification.
type QualityComparison struct {
	BaselineGeneratedAt  time.Time        `json:"baselineGeneratedAt"`
	Regressed            bool             `json:"regressed"`
	Score                IntDelta         `json:"score"`
	Tests                IntDelta         `json:"tests"`
	Failures             IntDelta         `json:"failures"`
	DurationMs           FloatDelta       `json:"durationMs"`
	LineCoveragePct      FloatDelta       `json:"lineCoveragePct"`
	BranchCoveragePct    FloatDelta       `json:"branchCoveragePct"`
	NewFailures          []TestChange     `json:"newFailures,omitempty"`
	ResolvedFailures     []TestChange     `json:"resolvedFailures,omitempty"`
	CoverageRegressions  []CoverageChange `json:"coverageRegressions,omitempty"`
	CoverageImprovements []CoverageChange `json:"coverageImprovements,omitempty"`
}

// IntDelta and FloatDelta make before/after comparisons explicit for agents.
type IntDelta struct {
	Before int `json:"before"`
	After  int `json:"after"`
	Delta  int `json:"delta"`
}

type FloatDelta struct {
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	Delta  float64 `json:"delta"`
}

// TestChange describes a test status transition between baseline and current.
type TestChange struct {
	Language       string `json:"language,omitempty"`
	Suite          string `json:"suite"`
	Classname      string `json:"classname,omitempty"`
	Name           string `json:"name"`
	PreviousStatus string `json:"previousStatus,omitempty"`
	CurrentStatus  string `json:"currentStatus,omitempty"`
}

// CoverageChange is a per-file line coverage movement.
type CoverageChange struct {
	Path      string  `json:"path"`
	Language  string  `json:"language,omitempty"`
	BeforePct float64 `json:"beforePct"`
	AfterPct  float64 `json:"afterPct"`
	DeltaPct  float64 `json:"deltaPct"`
}

// QualityHistory summarizes multiple prior reports plus the current run.
// FlakyTests is populated only when a test has at least three observations and
// has both passed and failed/errored.
type QualityHistory struct {
	Runs            int             `json:"runs"`
	WindowStart     time.Time       `json:"windowStart"`
	WindowEnd       time.Time       `json:"windowEnd"`
	QualityScore    TrendMetric     `json:"qualityScore"`
	LineCoveragePct TrendMetric     `json:"lineCoveragePct"`
	TestDurationMs  TrendMetric     `json:"testDurationMs"`
	Tests           TrendMetric     `json:"tests"`
	Failures        TrendMetric     `json:"failures"`
	Samples         []HistorySample `json:"samples"`
	FlakyTests      []FlakyTest     `json:"flakyTests,omitempty"`
}

// TrendMetric provides a simple least-squares slope per run and the observed
// first/last movement. Direction is increasing, decreasing, or stable.
type TrendMetric struct {
	First     float64 `json:"first"`
	Last      float64 `json:"last"`
	Delta     float64 `json:"delta"`
	Slope     float64 `json:"slope"`
	Direction string  `json:"direction"`
}

// HistorySample is one compact point used by trend visualizations.
type HistorySample struct {
	GeneratedAt     time.Time `json:"generatedAt"`
	QualityScore    int       `json:"qualityScore"`
	LineCoveragePct float64   `json:"lineCoveragePct"`
	Tests           int       `json:"tests"`
	Failures        int       `json:"failures"`
	DurationMs      float64   `json:"durationMs"`
}

// FlakyTest holds repeated status evidence rather than inferring flakiness from
// a single failure or a two-point baseline comparison.
type FlakyTest struct {
	Language       string   `json:"language,omitempty"`
	Suite          string   `json:"suite"`
	Classname      string   `json:"classname,omitempty"`
	Name           string   `json:"name"`
	Observations   int      `json:"observations"`
	Passed         int      `json:"passed"`
	Failed         int      `json:"failed"`
	Skipped        int      `json:"skipped"`
	FlakeRatePct   float64  `json:"flakeRatePct"`
	RecentStatuses []string `json:"recentStatuses"`
}

// ChangeAnalysis measures line-level coverage only for production source lines
// added or modified relative to an explicit Git base. Non-executable changed
// lines remain visible as UnmappedLines but do not dilute patch coverage.
type ChangeAnalysis struct {
	Base                 string                `json:"base"`
	FilesChanged         int                   `json:"filesChanged"`
	SourceFilesChanged   int                   `json:"sourceFilesChanged"`
	ChangedLines         int                   `json:"changedLines"`
	CoverableLines       int                   `json:"coverableLines"`
	CoveredLines         int                   `json:"coveredLines"`
	UncoveredLines       int                   `json:"uncoveredLines"`
	UnmappedLines        int                   `json:"unmappedLines"`
	FilesWithoutCoverage int                   `json:"filesWithoutCoverage"`
	CoveragePct          float64               `json:"coveragePct"`
	Files                []ChangedFileCoverage `json:"files,omitempty"`
}

// ChangedFileCoverage is the patch-coverage evidence for one changed
// production source file. UncoveredLineNumbers is capped for report size while
// UncoveredLines always contains the full count.
type ChangedFileCoverage struct {
	Path                 string  `json:"path"`
	Language             string  `json:"language,omitempty"`
	CoveragePath         string  `json:"coveragePath,omitempty"`
	ChangedLines         int     `json:"changedLines"`
	CoverableLines       int     `json:"coverableLines"`
	CoveredLines         int     `json:"coveredLines"`
	UncoveredLines       int     `json:"uncoveredLines"`
	UnmappedLines        int     `json:"unmappedLines"`
	CoveragePct          float64 `json:"coveragePct"`
	CoverageReported     bool    `json:"coverageReported"`
	CoverageRequired     bool    `json:"coverageRequired"`
	UncoveredLineNumbers []int   `json:"uncoveredLineNumbers,omitempty"`
}
