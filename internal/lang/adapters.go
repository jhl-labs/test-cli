package lang

// The adapter definitions below encode sensible defaults for each ecosystem.
// Commands write their native artifacts into the per-language raw output
// directory ({out}) so that ingestion is deterministic. Every command can be
// overridden per project via .test-cli.yaml (see internal/config).

func python() *Adapter {
	return &Adapter{
		Name:    "python",
		Title:   "Python",
		Markers: []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "tox.ini", "Pipfile"},
		Globs:   []string{"*.py"},
		Commands: []Command{{
			Args: []string{
				"pytest",
				"--junitxml={out}/junit.xml",
				"--cov=.",
				"--cov-report=xml:{out}/coverage.xml",
			},
		}},
		TestGlobs: []string{"junit.xml"},
		CovGlobs:  []string{"coverage.xml"},
		Doctor:    []string{"pytest", "python3", "python"},
		DocsURL:   "https://docs.pytest.org/",
	}
}

func typescript() *Adapter {
	return &Adapter{
		Name:    "typescript",
		Title:   "TypeScript / JavaScript",
		Markers: []string{"package.json", "tsconfig.json"},
		Commands: []Command{{
			Args: []string{
				"npx", "--yes", "jest",
				"--ci",
				"--reporters=default",
				"--reporters=jest-junit",
				"--coverage",
				"--coverageReporters=cobertura",
				"--coverageDirectory={out}",
			},
		}},
		// jest-junit honors JEST_JUNIT_OUTPUT_FILE (set by the runner); cobertura
		// lands in the coverage directory.
		TestGlobs: []string{"junit.xml"},
		CovGlobs:  []string{"cobertura-coverage.xml", "coverage.xml"},
		Doctor:    []string{"npx", "node"},
		DocsURL:   "https://jestjs.io/",
	}
}

func golang() *Adapter {
	return &Adapter{
		Name:    "go",
		Title:   "Go",
		Markers: []string{"go.mod"},
		Commands: []Command{{
			Args:   []string{"go", "test", "./...", "-json", "-coverprofile={out}/coverage.out"},
			Stdout: "gotest.json",
		}},
		TestGlobs: []string{"gotest.json"},
		CovGlobs:  []string{"coverage.out"},
		Doctor:    []string{"go"},
		DocsURL:   "https://pkg.go.dev/testing",
	}
}

func rust() *Adapter {
	return &Adapter{
		Name:    "rust",
		Title:   "Rust",
		Markers: []string{"Cargo.toml"},
		Commands: []Command{{
			// cargo-llvm-cov drives nextest and emits Cobertura in one pass.
			Args: []string{
				"cargo", "llvm-cov", "--cobertura",
				"--output-path", "{out}/coverage.xml",
				"nextest",
			},
		}},
		// nextest writes JUnit when configured; also accept the common location.
		TestGlobs: []string{"junit.xml", "target/nextest/*/junit.xml"},
		CovGlobs:  []string{"coverage.xml"},
		Doctor:    []string{"cargo"},
		DocsURL:   "https://nexte.st/",
	}
}

func csharp() *Adapter {
	return &Adapter{
		Name:  "csharp",
		Title: "C# / .NET",
		Globs: []string{"*.csproj", "*.sln", "*.fsproj"},
		Commands: []Command{{
			Args: []string{
				"dotnet", "test",
				"--logger", "junit;LogFilePath={out}/junit.xml",
				"--collect", "XPlat Code Coverage",
				"--results-directory", "{out}",
			},
		}},
		TestGlobs: []string{"junit.xml"},
		CovGlobs:  []string{"**/coverage.cobertura.xml", "*/coverage.cobertura.xml", "coverage.cobertura.xml"},
		Doctor:    []string{"dotnet"},
		DocsURL:   "https://learn.microsoft.com/dotnet/core/testing/",
	}
}

func java() *Adapter {
	return &Adapter{
		Name:    "java",
		Title:   "Java / Kotlin (JVM)",
		Markers: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle"},
		Commands: []Command{{
			// Maven surefire + jacoco. Gradle users override via .test-cli.yaml.
			Args: []string{"mvn", "-B", "test", "jacoco:report"},
		}},
		TestGlobs: []string{
			"target/surefire-reports/TEST-*.xml",
			"**/surefire-reports/TEST-*.xml",
			"build/test-results/test/TEST-*.xml",
		},
		CovGlobs: []string{
			"target/site/jacoco/jacoco.xml",
			"**/jacoco/jacoco.xml",
			"build/reports/jacoco/test/jacocoTestReport.xml",
		},
		Doctor:  []string{"mvn", "gradle", "java"},
		DocsURL: "https://www.jacoco.org/jacoco/",
	}
}
