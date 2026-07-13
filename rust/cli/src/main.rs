//! Prism CLI - local deterministic review before git push.

use clap::{Parser, Subcommand};
use diff_parser::parse;
use rules_engine::{evaluate, load_config};
use std::io::{self, Read};
use std::process::{Command, ExitCode};

#[derive(Parser)]
#[command(name = "prism", about = "Prism - self-hosted AI code review agent (local CLI)")]
struct Cli {
    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand)]
enum Commands {
    /// Run deterministic rules against a git diff (stdin or working tree)
    Check {
        /// Path to rules.toml
        #[arg(short, long, default_value = "config/examples/rules.toml")]
        config: String,

        /// Read diff from stdin instead of running `git diff`
        #[arg(long)]
        stdin: bool,

        /// Diff staged changes only (`git diff --cached`)
        #[arg(long)]
        staged: bool,
    },
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    match cli.command {
        Commands::Check {
            config,
            stdin,
            staged,
        } => run_check(&config, stdin, staged),
    }
}

fn run_check(config_path: &str, from_stdin: bool, staged: bool) -> ExitCode {
    let cfg = match load_config(config_path) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("Failed to load config {config_path}: {e}");
            return ExitCode::from(2);
        }
    };

    let raw = if from_stdin {
        let mut buf = String::new();
        if let Err(e) = io::stdin().read_to_string(&mut buf) {
            eprintln!("Failed to read stdin: {e}");
            return ExitCode::from(2);
        }
        buf
    } else {
        match git_diff(staged) {
            Ok(d) => d,
            Err(e) => {
                eprintln!("Failed to get git diff: {e}");
                return ExitCode::from(2);
            }
        }
    };

    if raw.trim().is_empty() {
        println!("No changes to review.");
        return ExitCode::SUCCESS;
    }

    let diff = match parse(&raw) {
        Ok(d) => d,
        Err(e) => {
            eprintln!("Failed to parse diff: {e}");
            return ExitCode::from(2);
        }
    };

    println!(
        "Prism check - {} file(s), +{} / -{} lines",
        diff.files.len(),
        diff.added_lines(),
        diff.removed_lines()
    );

    let result = evaluate(&diff, &cfg);

    if result.findings.is_empty() {
        println!("No rule violations found.");
        return ExitCode::SUCCESS;
    }

    for f in &result.findings {
        let loc = match f.line {
            Some(n) => format!("{}:{}", f.file, n),
            None => f.file.clone(),
        };
        let level = match f.severity {
            rules_engine::Severity::Critical => "CRITICAL",
            rules_engine::Severity::Warning => "WARNING",
        };
        println!("[{level}] ({}) {} @ {}", f.category, f.rule, loc);
        println!("         matched: {}", f.matched.trim());
    }

    println!(
        "\nSummary: {} critical, {} warning(s)",
        result.critical_count(),
        result.warning_count()
    );

    if cfg.behavior.block_on_critical && result.has_critical() {
        eprintln!("Blocked: critical issues found.");
        ExitCode::from(1)
    } else {
        ExitCode::SUCCESS
    }
}

fn git_diff(staged: bool) -> Result<String, String> {
    let mut cmd = Command::new("git");
    cmd.arg("diff");
    if staged {
        cmd.arg("--cached");
    }
    let output = cmd
        .output()
        .map_err(|e| format!("failed to run git: {e}"))?;
    if !output.status.success() {
        return Err(String::from_utf8_lossy(&output.stderr).into_owned());
    }
    Ok(String::from_utf8_lossy(&output.stdout).into_owned())
}
