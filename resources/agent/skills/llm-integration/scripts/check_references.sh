#!/usr/bin/env bash
set -euo pipefail

skill_dir="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)}"

if [[ ! -f "$skill_dir/SKILL.md" ]]; then
  printf 'Not an llm-integration skill directory: %s\n' "$skill_dir" >&2
  exit 2
fi

failures=0

if ! perl - "$skill_dir/SKILL.md" "$(basename "$skill_dir")" <<'PERL'
use strict;
use warnings;

my ($file, $expected_name) = @ARGV;
open my $fh, '<', $file or die "cannot read $file: $!\n";
local $/;
my $text = <$fh>;

die "SKILL.md must start with YAML front matter\n"
    unless $text =~ /\A---\s*\n(.*?)^---\s*\n/ms;
my $front_matter = $1;
my ($name) = $front_matter =~ /^name:\s*([^\s#]+)\s*$/m;
die "SKILL.md front matter is missing name\n" unless defined $name;
die "SKILL.md name '$name' does not match '$expected_name'\n"
    unless $name eq $expected_name;
die "SKILL.md front matter is missing description\n"
    unless $front_matter =~ /^description:\s*(?:\S.*)?$/m;
my ($description) = $front_matter =~ /^description:\s*(?:[|>][-+]?\s*)?\n((?:[ \t]+.*\n?)*)/m;
$description //= '';
$description =~ s/^\s+//mg;
die "SKILL.md description exceeds 1024 characters\n" if length($description) > 1024;

for my $section (
    '## Scope:',
    '## Routing:',
    '## Cross-provider rules',
    '## Agent workflow',
    '## When docs drift',
) {
    die "SKILL.md is missing required section '$section'\n"
        unless index($text, $section) >= 0;
}
PERL
then
  failures=1
fi

while IFS= read -r -d '' file; do
  relative="${file#"$skill_dir/"}"
  case "$relative" in
    SKILL.md|references/*|templates/*|scripts/*|assets/*) ;;
    *)
      printf 'Unsupported skill support path: %s\n' "$relative"
      failures=1
      ;;
  esac
done < <(find "$skill_dir" -type f -print0)

while IFS= read -r -d '' file; do
  if ! grep -qE '^# ' "$file"; then
    printf 'Reference is missing a level-one heading: %s\n' "$file"
    failures=1
  fi
  if ! perl - "$file" <<'PERL'
use strict;
use warnings;
use File::Basename qw(dirname);
use File::Spec;

my $file = shift @ARGV or die "missing file argument\n";
open my $fh, '<', $file or die "cannot read $file: $!\n";
local $/;
my $text = <$fh>;
my $bad = 0;

while ($text =~ /\]\(([^)\n]+)\)/g) {
    my $target = $1;
    $target =~ s/#.*\z//;
    next if $target eq '' || $target =~ m{^(?:https?://|mailto:|/)};

    my $resolved = File::Spec->rel2abs($target, dirname($file));
    next if -e $resolved;

    print "Missing relative link: $file -> $target\n";
    $bad = 1;
}

exit $bad;
PERL
  then
    failures=1
  fi
done < <(find "$skill_dir" -type f -name '*.md' -print0)

if command -v rg >/dev/null 2>&1; then
  while IFS= read -r token; do
    path="${token//\`/}"
    [[ "$path" == references/* ]] || continue
    [[ "$path" == *'{'* || "$path" == *'…'* || "$path" == *'<'* ]] && continue
    if [[ ! -e "$skill_dir/$path" ]]; then
      printf 'Missing routed reference: %s\n' "$path"
      failures=1
    fi
  done < <(rg -o '`references/[^` ]+`' "$skill_dir/SKILL.md" | sort -u)
else
  printf 'rg is required for routed-reference checks\n' >&2
  failures=1
fi

sample_dir="$skill_dir/scripts/samples"
if [[ -d "$sample_dir" ]]; then
  for sample in \
    "$sample_dir/README.md" \
    "$sample_dir/openai-responses/main.go" \
    "$sample_dir/openai-responses/openai.go" \
    "$sample_dir/openai-responses/main_test.go" \
    "$sample_dir/openai-responses/web/index.html" \
    "$sample_dir/openai-responses/web/app.js" \
    "$sample_dir/openai-responses/web/style.css"; do
    if [[ ! -f "$sample" ]]; then
      printf 'Missing runnable sample: %s\n' "${sample#"$skill_dir/"}"
      failures=1
    fi
  done
  if rg -n --glob '*.mjs' --glob '*.go' 'sk-[A-Za-z0-9_-]{12,}' "$sample_dir" >/dev/null 2>&1; then
    printf 'Sample contains a credential-looking literal; use an environment variable.\n'
    failures=1
  fi
fi

if (( failures )); then
  printf 'Reference checks failed.\n' >&2
  exit 1
fi

printf 'Skill metadata, reference links, and routed paths are valid.\n'
