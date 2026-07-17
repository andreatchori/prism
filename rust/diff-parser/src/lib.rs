//! Unified diff parser for Prism.
//!
//! Parses standard unified diffs (as produced by `git diff` / GitHub PR diffs)
//! into structured file hunks and lines.

use std::fmt;

/// A complete parsed diff.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Diff {
    pub files: Vec<FileDiff>,
}

impl Diff {
    /// Total number of added lines across all files.
    pub fn added_lines(&self) -> usize {
        self.files.iter().map(|f| f.added_lines()).sum()
    }

    /// Total number of removed lines across all files.
    pub fn removed_lines(&self) -> usize {
        self.files.iter().map(|f| f.removed_lines()).sum()
    }

    /// All added line contents (trimmed of the leading `+`).
    pub fn added_content(&self) -> Vec<&str> {
        self.files.iter().flat_map(|f| f.added_content()).collect()
    }
}

/// A single file within a diff.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FileDiff {
    pub old_path: String,
    pub new_path: String,
    pub hunks: Vec<Hunk>,
    pub is_new: bool,
    pub is_deleted: bool,
}

impl FileDiff {
    pub fn path(&self) -> &str {
        if self.new_path != "/dev/null" && !self.new_path.is_empty() {
            &self.new_path
        } else {
            &self.old_path
        }
    }

    pub fn added_lines(&self) -> usize {
        self.hunks
            .iter()
            .flat_map(|h| &h.lines)
            .filter(|l| matches!(l.kind, LineKind::Added))
            .count()
    }

    pub fn removed_lines(&self) -> usize {
        self.hunks
            .iter()
            .flat_map(|h| &h.lines)
            .filter(|l| matches!(l.kind, LineKind::Removed))
            .count()
    }

    pub fn added_content(&self) -> Vec<&str> {
        self.hunks
            .iter()
            .flat_map(|h| &h.lines)
            .filter(|l| matches!(l.kind, LineKind::Added))
            .map(|l| l.content.as_str())
            .collect()
    }
}

/// A hunk (`@@ -a,b +c,d @@`) within a file diff.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Hunk {
    pub old_start: u32,
    pub old_count: u32,
    pub new_start: u32,
    pub new_count: u32,
    pub header: String,
    pub lines: Vec<DiffLine>,
}

/// A single line inside a hunk.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiffLine {
    pub kind: LineKind,
    pub content: String,
    /// Line number in the new file (for Added / Context). None for Removed.
    pub new_line_no: Option<u32>,
    /// Line number in the old file (for Removed / Context). None for Added.
    pub old_line_no: Option<u32>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LineKind {
    Context,
    Added,
    Removed,
}

/// Errors produced while parsing a diff.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ParseError {
    EmptyInput,
    InvalidHunkHeader(String),
}

impl fmt::Display for ParseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            ParseError::EmptyInput => write!(f, "diff input is empty"),
            ParseError::InvalidHunkHeader(h) => write!(f, "invalid hunk header: {h}"),
        }
    }
}

impl std::error::Error for ParseError {}

/// Parse a unified diff string into a [`Diff`].
pub fn parse(input: &str) -> Result<Diff, ParseError> {
    if input.trim().is_empty() {
        return Err(ParseError::EmptyInput);
    }

    let mut files = Vec::new();
    let mut current: Option<FileDiff> = None;
    let mut current_hunk: Option<Hunk> = None;
    let mut new_line: u32 = 0;
    let mut old_line: u32 = 0;

    let flush_hunk = |file: &mut FileDiff, hunk: &mut Option<Hunk>| {
        if let Some(h) = hunk.take() {
            file.hunks.push(h);
        }
    };

    let flush_file =
        |files: &mut Vec<FileDiff>, file: &mut Option<FileDiff>, hunk: &mut Option<Hunk>| {
            if let Some(mut f) = file.take() {
                flush_hunk(&mut f, hunk);
                files.push(f);
            }
        };

    for raw in input.lines() {
        let line = raw.strip_suffix('\r').unwrap_or(raw);

        if line.starts_with("diff --git ") {
            flush_file(&mut files, &mut current, &mut current_hunk);
            current = Some(FileDiff {
                old_path: String::new(),
                new_path: String::new(),
                hunks: Vec::new(),
                is_new: false,
                is_deleted: false,
            });
            continue;
        }

        if line.starts_with("--- ") {
            let path = strip_diff_path(&line[4..]);
            if current.is_none() {
                current = Some(FileDiff {
                    old_path: path.clone(),
                    new_path: String::new(),
                    hunks: Vec::new(),
                    is_new: path == "/dev/null",
                    is_deleted: false,
                });
            } else if let Some(f) = current.as_mut() {
                f.old_path = path.clone();
                f.is_new = path == "/dev/null";
            }
            continue;
        }

        if line.starts_with("+++ ") {
            let path = strip_diff_path(&line[4..]);
            if let Some(f) = current.as_mut() {
                f.new_path = path.clone();
                f.is_deleted = path == "/dev/null";
            } else {
                current = Some(FileDiff {
                    old_path: String::new(),
                    new_path: path.clone(),
                    hunks: Vec::new(),
                    is_new: false,
                    is_deleted: path == "/dev/null",
                });
            }
            continue;
        }

        if line.starts_with("@@ ") {
            if let Some(f) = current.as_mut() {
                flush_hunk(f, &mut current_hunk);
            }
            let hunk = parse_hunk_header(line)?;
            new_line = hunk.new_start;
            old_line = hunk.old_start;
            current_hunk = Some(hunk);
            continue;
        }

        // Skip metadata lines that are not hunk content
        if current_hunk.is_none() {
            continue;
        }

        if let Some(hunk) = current_hunk.as_mut() {
            if let Some(diff_line) = parse_diff_line(line, &mut old_line, &mut new_line) {
                hunk.lines.push(diff_line);
            }
        }
    }

    flush_file(&mut files, &mut current, &mut current_hunk);

    // Handle diffs that only contain --- / +++ / @@ without diff --git
    if files.is_empty() {
        return Err(ParseError::EmptyInput);
    }

    Ok(Diff { files })
}

fn strip_diff_path(path: &str) -> String {
    let path = path.trim();
    // Strip optional "a/" or "b/" prefixes and trailing timestamps
    let path = path.split('\t').next().unwrap_or(path);
    if path == "/dev/null" {
        return path.to_string();
    }
    if let Some(rest) = path.strip_prefix("a/") {
        return rest.to_string();
    }
    if let Some(rest) = path.strip_prefix("b/") {
        return rest.to_string();
    }
    path.to_string()
}

fn parse_hunk_header(line: &str) -> Result<Hunk, ParseError> {
    // @@ -old_start,old_count +new_start,new_count @@ optional context
    let rest = line
        .strip_prefix("@@ ")
        .ok_or_else(|| ParseError::InvalidHunkHeader(line.to_string()))?;

    let mut parts = rest.split(" @@");
    let ranges = parts
        .next()
        .ok_or_else(|| ParseError::InvalidHunkHeader(line.to_string()))?;

    let mut old_start = 0u32;
    let mut old_count = 1u32;
    let mut new_start = 0u32;
    let mut new_count = 1u32;

    for token in ranges.split_whitespace() {
        if let Some(spec) = token.strip_prefix('-') {
            let (s, c) = parse_range(spec);
            old_start = s;
            old_count = c;
        } else if let Some(spec) = token.strip_prefix('+') {
            let (s, c) = parse_range(spec);
            new_start = s;
            new_count = c;
        }
    }

    if old_start == 0 && new_start == 0 {
        return Err(ParseError::InvalidHunkHeader(line.to_string()));
    }

    Ok(Hunk {
        old_start,
        old_count,
        new_start,
        new_count,
        header: line.to_string(),
        lines: Vec::new(),
    })
}

fn parse_range(spec: &str) -> (u32, u32) {
    if let Some((start, count)) = spec.split_once(',') {
        (start.parse().unwrap_or(0), count.parse().unwrap_or(1))
    } else {
        (spec.parse().unwrap_or(0), 1)
    }
}

fn parse_diff_line(line: &str, old_line: &mut u32, new_line: &mut u32) -> Option<DiffLine> {
    if line.is_empty() {
        return None;
    }
    // "\ No newline at end of file"
    if line.starts_with('\\') {
        return None;
    }

    let (kind, content) = match line.as_bytes()[0] {
        b'+' => (LineKind::Added, &line[1..]),
        b'-' => (LineKind::Removed, &line[1..]),
        b' ' => (LineKind::Context, &line[1..]),
        _ => return None,
    };

    let (old_line_no, new_line_no) = match kind {
        LineKind::Added => {
            let n = *new_line;
            *new_line += 1;
            (None, Some(n))
        }
        LineKind::Removed => {
            let n = *old_line;
            *old_line += 1;
            (Some(n), None)
        }
        LineKind::Context => {
            let o = *old_line;
            let n = *new_line;
            *old_line += 1;
            *new_line += 1;
            (Some(o), Some(n))
        }
    };

    Some(DiffLine {
        kind,
        content: content.to_string(),
        new_line_no,
        old_line_no,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    const SAMPLE: &str = r#"diff --git a/src/main.go b/src/main.go
index 111..222 100644
--- a/src/main.go
+++ b/src/main.go
@@ -1,5 +1,6 @@
 package main
 
+import "fmt"
 
 func main() {
-    // old
+    fmt.Println("hello")
 }
"#;

    #[test]
    fn parses_sample_diff() {
        let diff = parse(SAMPLE).expect("parse");
        assert_eq!(diff.files.len(), 1);
        assert_eq!(diff.files[0].path(), "src/main.go");
        assert_eq!(diff.added_lines(), 2);
        assert_eq!(diff.removed_lines(), 1);
        let added = diff.added_content();
        assert!(added.iter().any(|l| l.contains("fmt.Println")));
    }

    #[test]
    fn parses_new_file() {
        let input = r#"diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
"#;
        let diff = parse(input).unwrap();
        assert!(diff.files[0].is_new);
        assert_eq!(diff.added_lines(), 2);
    }

    #[test]
    fn empty_input_errors() {
        assert!(matches!(parse("   "), Err(ParseError::EmptyInput)));
    }
}
