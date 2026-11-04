# Reference File Templates

Templates for each reference file type in generated skills.

## Contents

- [core-thesis.md](#core-thesismd)
- [frameworks.md](#frameworksmd)
- [principles.md](#principlesmd)
- [techniques.md](#techniquesmd)
- [anti-patterns.md](#anti-patternsmd)
- [voice.md](#voicemd)
- [workflows/{action}-{topic}.md](#workflowsaction-topicmd)

## core-thesis.md

```markdown
# Core Thesis: {Book Title}

<central_argument>
{2-3 paragraphs capturing the book's main argument in the author's voice}
</central_argument>

<key_claims>

1. **{Claim}:** {Supporting logic}
2. **{Claim}:** {Supporting logic}
3. **{Claim}:** {Supporting logic}

</key_claims>

<contribution>
{What this book adds that didn't exist before}
</contribution>

<audience>
{Who benefits most from this thinking}
</audience>
```

## frameworks.md

```markdown
# Frameworks from {Book Title}

<framework name="{exact_name}">

## {Framework Name}

**The Insight:** {One sentence capturing why this matters}

**Why It Works:**
- {Reason 1}
- {Reason 2}
- {Reason 3}

**Structure:**
{Describe components - for a 2x2 matrix, list quadrants; for a cycle, list stages; etc.}

**Application:**
1. {How to apply step 1}
2. {How to apply step 2}
3. {How to apply step 3}

**Example:** {Concrete example from the book}

**When to use:** {Context where this framework fits}

**Common misapplication:** {How people get it wrong}

</framework>

<!-- Repeat for each framework -->
```

## principles.md

```markdown
# Principles from {Book Title}

<principle_collection>

## {Principle Name}

**Statement:** {Full articulation}

**Rationale:** {Why this is true}

**Application:** {How to apply it}

**Counter-example:** {When it doesn't apply, if any}

---

<!-- Repeat for each principle -->

</principle_collection>

<principle_summary>

Quick reference list:

1. **{Name}:** {One-line summary}
2. **{Name}:** {One-line summary}
3. **{Name}:** {One-line summary}

</principle_summary>
```

## techniques.md

```markdown
# Techniques from {Book Title}

<technique name="{technique_name}">

## {Technique Name}

**Purpose:** {What this accomplishes}

**Prerequisites:** {What's needed before starting}

**Steps:**

1. **{Step name}:** {Description}
2. **{Step name}:** {Description}
3. **{Step name}:** {Description}

**Duration:** {How long it takes}

**Frequency:** {How often to apply}

**Expected outcome:** {What success looks like}

**Variations:** {Adaptations for different contexts}

</technique>

<!-- Repeat for each technique -->
```

## anti-patterns.md

```markdown
# Anti-Patterns from {Book Title}

Warning patterns identified by {Author}.

<anti_pattern name="{pattern_name}">

## {Anti-Pattern Name}

**The Mistake:** {What people do wrong}

**Why It's Tempting:** {Why people fall into this}

**Why It Fails:** {The mechanism of failure}

**The Fix:** {What to do instead}

**Example:** {Case from the book showing the failure}

**Detection:** {How to notice you're doing this}

</anti_pattern>

<!-- Repeat for each anti-pattern -->

<quick_checklist>

Red flags to watch for:

- [ ] {Warning sign 1}
- [ ] {Warning sign 2}
- [ ] {Warning sign 3}

</quick_checklist>
```

## voice.md

```markdown
# Voice Calibration: {Author Name}

How {Author} thinks and communicates.

<sentence_patterns>

**Typical sentence length:** {Short/medium/long}

**Structure preferences:**
- {Pattern 1, e.g., "Short declarative statements followed by expansion"}
- {Pattern 2, e.g., "Rhetorical questions then answers"}

**Sample sentences:**
> "{Representative sentence 1}"

> "{Representative sentence 2}"

> "{Representative sentence 3}"

</sentence_patterns>

<example_style>

**Primary example type:** {Stories/Data/Analogies/Case studies}

**Example characteristics:**
- {Feature 1, e.g., "Names real companies"}
- {Feature 2, e.g., "Uses hypotheticals for sensitive topics"}
- {Feature 3, e.g., "Connects to reader's likely experience"}

</example_style>

<rhetorical_devices>

**Commonly used:**
- {Device 1, e.g., "Rule of three"}
- {Device 2, e.g., "Callback to earlier points"}
- {Device 3, e.g., "Contrarian opening"}

**Typical opening patterns:**
- "{How chapters/sections typically start}"

**Typical closing patterns:**
- "{How chapters/sections typically end}"

</rhetorical_devices>

<tone_calibration>

**Confidence level:** {Bold claims / Hedged with evidence / Academic caution}

**Formality:** {Conversational / Professional / Academic}

**Reader relationship:** {Teacher-student / Peer / Expert-to-practitioner}

**Distinctive elements:**
- {Unique aspect 1}
- {Unique aspect 2}

</tone_calibration>

<signature_phrases>

Terms and phrases unique to this author:

- "{Phrase 1}" - {meaning/usage}
- "{Phrase 2}" - {meaning/usage}
- "{Phrase 3}" - {meaning/usage}

</signature_phrases>

<writing_as_author>

When writing in {Author}'s voice:

DO:
- {Specific guidance 1}
- {Specific guidance 2}
- {Specific guidance 3}

DON'T:
- {What to avoid 1}
- {What to avoid 2}

</writing_as_author>
```

## workflows/{action}-{topic}.md

For skills that warrant specific workflows.

```markdown
# {Action} {Topic} - {Author} Method

Apply {Author}'s framework to {specific use case}.

<context>
When to use this workflow: {Triggering conditions}
</context>

<prerequisites>
- {What's needed before starting}
</prerequisites>

<steps>

## Step 1: {Name}

{Description}

**Apply:** {Which framework/principle to use here}

**Output:** {What this step produces}

---

## Step 2: {Name}

{Description}

**Apply:** {Which framework/principle to use here}

**Output:** {What this step produces}

---

## Step 3: {Name}

{Description}

**Apply:** {Which framework/principle to use here}

**Output:** {What this step produces}

</steps>

<completion>

**Success criteria:** {How to know you're done}

**Next steps:** {What typically follows}

</completion>
```
