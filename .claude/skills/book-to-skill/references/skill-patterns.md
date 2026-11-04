# Output Skill Structure Patterns

Templates and patterns for generating skills from books.

## Simple Skill Template

For books with < 5 frameworks or a single tight focus.

```markdown
---
name: {author-lastname}-{concept}
description: {One sentence describing what this skill helps accomplish. Start with verb.}
---

# {Book Title} - {Author Name}

<core_thesis>
{2-3 sentences capturing the book's central argument}
</core_thesis>

<frameworks>

## {Framework Name}

**The Insight:** {One sentence}

**Structure:**
{Describe the framework's components}

**Application:**
{How to use it in practice}

**Example from book:**
{Concrete example the author provides}

</frameworks>

<principles>

- **{Principle Name}:** {Brief explanation}
- **{Principle Name}:** {Brief explanation}

</principles>

<techniques>

## {Technique Name}

1. {Step one}
2. {Step two}
3. {Step three}

**When to use:** {Context}

</techniques>

<anti_patterns>

- **{Anti-pattern}:** {Why it fails}

</anti_patterns>

<voice_calibration>
{How the author thinks and writes - sentence patterns, rhetorical devices, typical examples}
</voice_calibration>
```

## Router Skill Template

For complex books with 5+ frameworks and multiple use cases.

### SKILL.md (Router)

```markdown
---
name: {author-lastname}-{concept}
description: {One sentence describing what this skill helps accomplish.}
---

# {Book Title} by {Author}

{2-3 sentence overview of the book's contribution}

<essential_principle>
{The single most important insight - in the author's voice}
</essential_principle>

<routing>

## Available Contexts

### Writing/Communication
Use when crafting content in the author's style
→ Load: `references/voice.md`

### Framework Application
Use when solving problems with author's mental models
→ Load: `references/frameworks.md`

### Decision Making
Use when applying principles to choices
→ Load: `references/principles.md`, `references/anti-patterns.md`

### Building/Creating
Use when constructing something using author's approach
→ Load: `references/techniques.md`

</routing>

<quick_reference>

## Top 3 Frameworks
1. **{Name}:** {One-line summary}
2. **{Name}:** {One-line summary}
3. **{Name}:** {One-line summary}

## Core Principles
- {Principle one}
- {Principle two}
- {Principle three}

</quick_reference>
```

## File Organization Patterns

### By Content Type (Default)
```
references/
├── core-thesis.md      # Central argument
├── frameworks.md       # All mental models
├── principles.md       # Guiding rules
├── techniques.md       # Step-by-step methods
├── anti-patterns.md    # What to avoid
└── voice.md           # Communication style
```

### By Topic (For broad books)
```
references/
├── core-thesis.md
├── leadership/
│   ├── frameworks.md
│   └── techniques.md
├── strategy/
│   ├── frameworks.md
│   └── techniques.md
└── voice.md
```

### By Application (For methodologies)
```
references/
├── core-thesis.md
├── diagnosis.md        # Understanding the problem
├── design.md          # Planning the solution
├── execution.md       # Implementing
├── evaluation.md      # Measuring results
└── voice.md
```

## Naming Conventions

### Skill Name
- Format: `{author-lastname}-{core-concept}`
- Examples:
  - `cialdini-influence`
  - `meadows-systems`
  - `newport-deepwork`
  - `christensen-disruption`

### Framework Names
- Preserve author's exact naming
- If unnamed, create: `The {Author} {Concept} {Structure}`
- Example: "The Collins Hedgehog Concept"

### File Names
- Lowercase with hyphens
- Descriptive but brief
- Match content focus: `decision-frameworks.md`, `writing-techniques.md`
