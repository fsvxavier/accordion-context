# Analyze Book (No Generation)

Workflow for deep analysis without skill generation. Useful when user wants to review extractions before committing to a skill structure.

## When to Use

- User says "just analyze" or "analyze only"
- User wants to review before generating
- User plans to manually curate the extraction
- Book is unusual and needs human judgment on structure

## Workflow Steps

### Step 1: Initial Survey

**Actions:**
1. Verify file exists and is readable
2. Read opening (title, author, TOC, preface)
3. Read closing (conclusion, summary)
4. Estimate scope

**Output to user:**
```
## Book Overview

**Title:** {title}
**Author:** {author}
**Estimated chapters:** {count}
**Estimated length:** {word count or page estimate}

**Initial observations:**
- {Structure type: academic, business, narrative, etc.}
- {Apparent framework density: high/medium/low}

Proceeding with full analysis...
```

---

### Step 2: Deep Extraction

**For each chapter/section:**

Extract all items per `references/extraction-patterns.md`:
- Frameworks (with exact names, components, applications)
- Principles (with rationales)
- Techniques (with steps)
- Anti-patterns (with failure mechanisms)
- Voice samples (representative sentences)

Track source locations (chapter/section) for each item.

---

### Step 3: Synthesis

**Actions:**
1. Merge and deduplicate across chapters
2. Rank by importance:
   - Frequency of reference
   - Structural prominence
   - Author emphasis
3. Identify relationships between frameworks
4. Note thematic clusters

---

### Step 4: Present Analysis Report

**Output comprehensive report:**

```
# Analysis Report: {Book Title}

## Executive Summary
{3-4 sentences on the book's core contribution and structure}

---

## Frameworks ({count})

### Tier 1: Core Frameworks
{Frameworks that are central to the book's thesis}

**{Framework Name}**
- Type: {Matrix/Cycle/Hierarchy/Process/etc.}
- Components: {List}
- Key insight: {Why it matters}
- Chapters referenced: {List}

### Tier 2: Supporting Frameworks
{Frameworks that extend or apply the core ideas}

...

---

## Principles ({count})

### Core Principles
{Principles repeated throughout or emphasized in conclusion}

1. **{Name}:** {Statement}
   - Rationale: {Why}
   - Source: {Chapter}

### Secondary Principles
...

---

## Techniques ({count})

**{Technique Name}**
- Purpose: {What it accomplishes}
- Steps: {Count}
- Source: {Chapter}

...

---

## Anti-Patterns ({count})

**{Anti-Pattern Name}**
- The mistake: {What}
- Why it fails: {Mechanism}
- Source: {Chapter}

...

---

## Voice Analysis

**Sentence characteristics:**
- {Observation}

**Example style:**
- {Observation}

**Rhetorical patterns:**
- {Observation}

**Sample sentences:**
> "{Example 1}"
> "{Example 2}"

---

## Skill Generation Recommendations

**Recommended structure:** {Simple/Router}

**Reasoning:** {Why this structure fits}

**Suggested skill name:** `{author}-{concept}`

**Recommended reference depth:** {Essential/Comprehensive/Exhaustive}

**Notes for generation:**
- {Any special considerations}
- {Frameworks that should be grouped}
- {Content that might be omitted}

---

## Raw Extraction Data

{Full extraction lists for user review, organized by chapter}
```

---

### Step 5: Offer Next Steps

**Ask user (via AskUserQuestion tool):**
- Question: "Analysis complete. What would you like to do next?"
- Options: Generate skill now | Adjust scope (include/exclude items) | Re-analyze specific sections | Save analysis to file

---

## Output Artifacts

This workflow produces:
- Structured analysis report (displayed to user)
- Extraction data suitable for `generate-skill.md` workflow

The analysis can be saved to a file if user requests, which can later be fed to the generate workflow.
