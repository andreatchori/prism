//! Deterministic rules engine for Prism.
//!
//! Evaluates configured rules against a parsed [`diff_parser::Diff`].
//! Critical findings come from `forbidden` and `security` rule groups.

use diff_parser::Diff;
use regex::Regex;
use serde::{Deserialize, Serialize};
use std::fmt;
use std::fs;
use std::path::Path;

/// Top-level rules configuration (compatible with Prism `rules.toml`).
#[derive(Debug, Clone, Deserialize, Default)]
pub struct RulesConfig {
    #[serde(default)]
    pub reviewer: Reviewer,
    #[serde(default)]
    pub rules: Rules,
    #[serde(default)]
    pub behavior: Behavior,
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct Reviewer {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub language: String,
    #[serde(default)]
    pub tone: String,
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct Rules {
    #[serde(default)]
    pub must_have: RuleSet,
    #[serde(default)]
    pub forbidden: RuleSet,
    #[serde(default)]
    pub security: RuleSet,
    #[serde(default)]
    pub performance: RuleSet,
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct RuleSet {
    #[serde(default)]
    pub items: Vec<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Behavior {
    #[serde(default = "default_true")]
    pub block_on_critical: bool,
    #[serde(default = "default_true")]
    pub suggest_fixes: bool,
    #[serde(default = "default_true")]
    pub praise_good_code: bool,
    #[serde(default = "default_max_diff")]
    pub max_diff_lines: usize,
}

fn default_true() -> bool {
    true
}

fn default_max_diff() -> usize {
    10_000
}

impl Default for Behavior {
    fn default() -> Self {
        Self {
            block_on_critical: true,
            suggest_fixes: true,
            praise_good_code: true,
            max_diff_lines: 10_000,
        }
    }
}

/// Severity of a rule finding.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Severity {
    Critical,
    Warning,
}

/// A single rule violation found in the diff.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Finding {
    pub severity: Severity,
    pub category: String,
    pub rule: String,
    pub file: String,
    pub line: Option<u32>,
    pub matched: String,
}

/// Result of evaluating rules against a diff.
#[derive(Debug, Clone, Default, Serialize)]
pub struct Evaluation {
    pub findings: Vec<Finding>,
}

impl Evaluation {
    pub fn has_critical(&self) -> bool {
        self.findings
            .iter()
            .any(|f| f.severity == Severity::Critical)
    }

    pub fn critical_count(&self) -> usize {
        self.findings
            .iter()
            .filter(|f| f.severity == Severity::Critical)
            .count()
    }

    pub fn warning_count(&self) -> usize {
        self.findings
            .iter()
            .filter(|f| f.severity == Severity::Warning)
            .count()
    }
}

#[derive(Debug)]
pub enum EngineError {
    Io(std::io::Error),
    Toml(toml::de::Error),
}

impl fmt::Display for EngineError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            EngineError::Io(e) => write!(f, "io error: {e}"),
            EngineError::Toml(e) => write!(f, "toml error: {e}"),
        }
    }
}

impl std::error::Error for EngineError {}

impl From<std::io::Error> for EngineError {
    fn from(e: std::io::Error) -> Self {
        EngineError::Io(e)
    }
}

impl From<toml::de::Error> for EngineError {
    fn from(e: toml::de::Error) -> Self {
        EngineError::Toml(e)
    }
}

/// Load rules from a TOML file.
pub fn load_config(path: impl AsRef<Path>) -> Result<RulesConfig, EngineError> {
    let data = fs::read_to_string(path)?;
    let cfg: RulesConfig = toml::from_str(&data)?;
    Ok(cfg)
}

/// Evaluate deterministic pattern rules against a parsed diff.
///
/// Heuristics:
/// - `forbidden` / `security` rules: if an added line matches a derived pattern, emit Critical
/// - `must_have` / `performance`: warning-level substring / keyword hints on added lines
pub fn evaluate(diff: &Diff, cfg: &RulesConfig) -> Evaluation {
    let mut findings = Vec::new();

    for file in &diff.files {
        for hunk in &file.hunks {
            for line in &hunk.lines {
                if line.kind != diff_parser::LineKind::Added {
                    continue;
                }
                let content = &line.content;
                let line_no = line.new_line_no;

                for rule in &cfg.rules.forbidden.items {
                    if line_matches(content, rule) {
                        findings.push(Finding {
                            severity: Severity::Critical,
                            category: "forbidden".into(),
                            rule: rule.clone(),
                            file: file.path().to_string(),
                            line: line_no,
                            matched: content.clone(),
                        });
                    }
                }

                for rule in &cfg.rules.security.items {
                    if line_matches(content, rule) {
                        findings.push(Finding {
                            severity: Severity::Critical,
                            category: "security".into(),
                            rule: rule.clone(),
                            file: file.path().to_string(),
                            line: line_no,
                            matched: content.clone(),
                        });
                    }
                }

                for rule in &cfg.rules.performance.items {
                    if line_matches(content, rule) {
                        findings.push(Finding {
                            severity: Severity::Warning,
                            category: "performance".into(),
                            rule: rule.clone(),
                            file: file.path().to_string(),
                            line: line_no,
                            matched: content.clone(),
                        });
                    }
                }
            }
        }
    }

    Evaluation { findings }
}

/// Derive a practical matcher from a human-readable rule string.
fn line_matches(content: &str, rule: &str) -> bool {
    let lower = content.to_lowercase();
    let rule_l = rule.to_lowercase();

    // Explicit patterns for common Prism example rules
    if rule_l.contains("hardcoded") && (rule_l.contains("secret") || rule_l.contains("api key") || rule_l.contains("password"))
    {
        return looks_like_secret(&lower);
    }
    if rule_l.contains("unwrap()") {
        return lower.contains("unwrap()") || lower.contains(".unwrap(");
    }
    if rule_l.contains("debug print") || rule_l.contains("println") {
        return lower.contains("println!")
            || lower.contains("dbg!")
            || lower.contains("fmt.println")
            || lower.contains("console.log")
            || lower.contains("print(");
    }
    if rule_l.contains("variable named") {
        return bad_variable_name(&lower);
    }
    if rule_l.contains("sql") && rule_l.contains("prepared") {
        return looks_like_raw_sql(&lower);
    }
    if rule_l.contains("sensitive") && rule_l.contains("log") {
        return lower.contains("password") && (lower.contains("log") || lower.contains("println"));
    }

    // Fallback: extract quoted keywords or notable tokens from the rule
    for token in extract_keywords(rule) {
        if lower.contains(&token.to_lowercase()) {
            return true;
        }
    }

    false
}

fn looks_like_secret(line: &str) -> bool {
    let patterns = [
        r#"(?i)(api[_-]?key|secret|password|token)\s*:?=\s*['"][^'"]{8,}['"]"#,
        r#"(?i)(api[_-]?key|secret|password|token)\s*:?=\s*`[^`]{8,}`"#,
        r#"(?i)bearer\s+[a-z0-9\-_\.]{20,}"#,
    ];
    for p in patterns {
        if let Ok(re) = Regex::new(p) {
            if re.is_match(line) {
                return true;
            }
        }
    }
    false
}

fn bad_variable_name(line: &str) -> bool {
    let re = Regex::new(r"(?i)\b(temp|data|test|buf|foo)\b\s*:=").ok();
    if let Some(re) = re {
        if re.is_match(line) {
            return true;
        }
    }
    let re2 = Regex::new(r#"(?i)\b(let|var|const)\s+(temp|data|test|buf|foo)\b"#).ok();
    re2.map(|r| r.is_match(line)).unwrap_or(false)
}

fn looks_like_raw_sql(line: &str) -> bool {
    let has_sql = line.contains("select ") || line.contains("insert ") || line.contains("update ") || line.contains("delete ");
    let looks_concat = line.contains("+") || line.contains("fmt.sprintf") || line.contains("format!(");
    has_sql && looks_concat
}

fn extract_keywords(rule: &str) -> Vec<String> {
    let mut out = Vec::new();
    for part in rule.split(|c: char| !c.is_alphanumeric() && c != '_' && c != '-') {
        let p = part.trim();
        if p.len() >= 4 {
            let lower = p.to_lowercase();
            // skip generic english words
            const SKIP: &[&str] = &[
                "must", "have", "always", "never", "without", "every", "function", "should",
                "these", "their", "about", "which", "from", "with", "that", "this", "into",
            ];
            if !SKIP.contains(&lower.as_str()) {
                out.push(p.to_string());
            }
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use diff_parser::parse;

    #[test]
    fn detects_hardcoded_secret() {
        let diff = parse(
            r#"diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,1 +1,2 @@
 package main
+apiKey := "sk-secret-value-123456"
"#,
        )
        .unwrap();

        let cfg = RulesConfig {
            rules: Rules {
                forbidden: RuleSet {
                    items: vec!["No hardcoded secrets, API keys or passwords".into()],
                },
                ..Default::default()
            },
            ..Default::default()
        };

        let result = evaluate(&diff, &cfg);
        assert!(result.has_critical());
        assert_eq!(result.critical_count(), 1);
    }

    #[test]
    fn clean_diff_passes() {
        let diff = parse(
            r#"diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,1 +1,2 @@
 package main
+const AppName = "prism"
"#,
        )
        .unwrap();

        let cfg = RulesConfig {
            rules: Rules {
                forbidden: RuleSet {
                    items: vec!["No hardcoded secrets, API keys or passwords".into()],
                },
                ..Default::default()
            },
            ..Default::default()
        };

        let result = evaluate(&diff, &cfg);
        assert!(!result.has_critical());
    }
}
