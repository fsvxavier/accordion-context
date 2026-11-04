# Convert Book to Skill

Main workflow for end-to-end book conversion.

## Prerequisites

- User has provided a `.txt` book file path
- File is readable and contains book content

## Workflow Steps

### Step 1: Validate and Survey

**Actions:**
1. Verify file exists and is readable
2. Read first ~500 lines to get:
   - Title and author
   - Table of contents (if present)
   - Preface/introduction
3. Read last ~200 lines to get:
   - Conclusion/summary
   - Index/bibliography
4. Estimate total size and chapter count

**Ask user (via AskUserQuestion tool):**
- Question: "What should this skill help you accomplish?"
- Options: Write like the author | Apply their frameworks to problems | Think with their mental models | Build using their approach | All of the above

**Output:** Book metadata, user's intent

---

### Step 2: Chapter-by-Chapter Analysis

**For each identifiable chapter/section:**

1. Read the chapter content
2. Extract using patterns from [references/extraction-patterns.md](../references/extraction-patterns.md):

**Frameworks:**
- Named mental models
- Visual structures described
- Multi-component systems

**Principles:**
- Imperative statements
- Core rules
- Repeated themes

**Techniques:**
- Step-by-step methods
- Processes with outcomes
- Specific practices

**Anti-patterns:**
- Warnings
- Common mistakes
- Failure cases

**Voice samples:**
- Representative sentences
- Rhetorical patterns
- Example styles

3. Note chapter-specific context for each extraction

**Output:** Per-chapter extraction lists

---

### Step 3: Synthesize Findings

**Actions:**
1. Merge extractions across chapters
2. Deduplicate similar frameworks/principles
3. Identify the top concepts by:
   - Frequency of reference
   - Emphasis in intro/conclusion
   - Structural prominence (chapter titles)
4. Rank frameworks by leverage (reusability × impact)
5. Identify voice patterns across samples

**Determine skill complexity:**
- Simple (< 5 frameworks, single focus) → Single SKILL.md
- Router (5+ frameworks, multiple applications) → Full structure with references

**Output:** Unified extraction report, complexity determination

---

### Step 4: Present Extraction Summary

**Show user:**
```
## Extraction Summary: {Book Title} by {Author}

### Frameworks Found ({count})
1. {Name}: {One-line description}
2. {Name}: {One-line description}
...

### Key Principles ({count})
1. {Principle}
2. {Principle}
...

### Techniques ({count})
1. {Name}: {Purpose}
...

### Anti-Patterns ({count})
1. {Name}: {What to avoid}
...

### Voice Characteristics
- {Key observation 1}
- {Key observation 2}

### Recommended Skill Type
{Simple/Router} because {rationale}
```

**Ask user (via AskUserQuestion tool):**
- Question: "Does this extraction look accurate? What should I do next?"
- Options: Proceed with all extracted content | Focus on specific frameworks | Re-analyze certain chapters | Add emphasis on something I missed

**Output:** User-approved extraction scope

---

### Step 5: Propose Skill Structure

**Enter plan mode.**

**Propose:**
```
## Proposed Skill: {author-lastname}-{concept}

### Description
{One sentence for frontmatter}

### Structure
{Simple or Router, with file list}

### Files to Generate
{List each file with brief content description}

### Reference Depth
{Essential/Comprehensive/Exhaustive}
```

**If router skill, show proposed file organization:**
```
{skill-name}/
├── SKILL.md
├── references/
│   ├── core-thesis.md
│   ├── frameworks.md
│   ├── principles.md
│   ├── techniques.md
│   ├── anti-patterns.md
│   └── voice.md
└── workflows/
    └── {any specific workflows}
```

**Ask user (via AskUserQuestion tool):**
- Question: "Does this skill structure work?"
- Options: Yes, generate the files | Adjust the structure first | Change the skill name | Change reference depth

**Output:** Approved skill structure

---

### Step 6: Generate Skill Files

**Exit plan mode and generate.**

**For simple skill:**
1. Create `{skill-name}/SKILL.md` using simple template from [references/skill-patterns.md](../references/skill-patterns.md)
2. Populate with all extracted content

**For router skill:**
1. Create directory structure
2. Generate SKILL.md with:
   - Frontmatter (name, description)
   - Essential principle
   - Routing logic
   - Quick reference
3. Generate each reference file using templates from [references/reference-file-templates.md](../references/reference-file-templates.md):
   - `core-thesis.md` - Central argument
   - `frameworks.md` - All extracted frameworks
   - `principles.md` - All principles
   - `techniques.md` - All techniques
   - `anti-patterns.md` - All warnings
   - `voice.md` - Voice calibration
4. Generate any workflows identified

**Quality checks during generation:**
- Preserve author's exact framework names
- Include concrete examples from book
- Maintain voice authenticity in samples
- Cross-reference between files where relevant

---

### Step 7: Validate and Report

**Actions:**
1. Verify all files created successfully
2. Check frontmatter is valid YAML
3. Confirm skill can be located

**Report to user:**
```
## Skill Generated: {skill-name}

**Location:** {path}

**Files created:**
- SKILL.md ({lines} lines)
- references/frameworks.md ({count} frameworks)
- references/principles.md ({count} principles)
- ...

**To use this skill:**
- Invoke with `/{skill-name}`
- Or reference in conversations about {topic}

**Extracted from {Book Title}:**
- {X} frameworks
- {Y} principles
- {Z} techniques
- {W} anti-patterns

**The skill is ready to use.**
```

---

## Error Handling

**File not found:**
```
"I couldn't find a file at {path}. Please check the path and try again."
```

**File too large:**
```
"This book is very large ({size}). I'll analyze it in sections. This may take several passes."
```

**Poor extraction yield:**
```
"I found fewer frameworks than expected. This book may be:
- More narrative than structural
- Better suited for voice extraction only
- Needing manual framework identification

Want me to proceed with what I found, or would you like to identify specific concepts to extract?"
```

**Ambiguous structure:**
```
"The book doesn't have clear chapter divisions. I'll analyze it by theme instead. This may result in some overlap in extractions."
```
