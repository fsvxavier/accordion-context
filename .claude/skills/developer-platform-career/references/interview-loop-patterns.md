# Interview loop patterns

Contents: the API-design-vs-general-system-design split, how PM and engineering loops differ, take-home practices, GitLab's real posted loop, and what's confirmed vs inferred.

## API design is a named, separate interview format from general system design

This is the clearest, most concretely sourced finding for this section. Multiple companies (Meta specifically named) now split what used to be one "system design" round into two distinct interview types:

- **System design** - large-scale distributed-systems architecture; "if you want to work on the functional building blocks of a software system."
- **API design** (reported internally at Meta as "Pirate X" in candidate-prep literature) - "if you want to leverage those building blocks to create products and features." Scope is deliberately smaller than full system design: no full architecture to sketch, focus is on design decisions and their downstream consequences for the developer consuming the API.

A commonly cited worked example: design a payments API where the scope is card-payment processing, the "users" are developers integrating the API into their own product, and the bar is that the API must be clear, predictable, and hard to misuse, with error messages specific enough that a developer can act on them. This is judged on developer-facing usability and misuse-resistance, not raw scale or availability trade-offs.

**For a PM candidate**, the bar differs by design from an engineering candidate's: PMs are assessed on how well they scope, structure, and communicate the design thinking, not on proving deep technical implementation expertise - echoed at Google, where system-design-flavored PM rounds emphasize estimation and scoping over the technical solution itself. Some PM loops still include a genuine technical conversation: Uber's onsite reportedly includes a system-design discussion with an engineer where the PM candidate must explain APIs and how a system fits together without hand-waving, though without writing code.

## Take-home and written components

Sourced practice across companies, not developer-platform-specific but directly relevant to how a technical PM loop is structured: Google closes its PM loop with a written product exercise; Meta opens with short-answer questions due back in about 24 hours; Roblox asks candidates to explain their assessment reasoning in a short essay; LinkedIn's product-sense round has historically included a writing sample. No developer-platform-specific take-home (for example, "design this SDK" as a formal artifact) was found as a named, sourced practice - treat "a take-home designing an SDK or API surface" as a plausible format by analogy to the API-design interview type above, not as a directly confirmed practice.

## A real, posted engineering-side loop: GitLab's Partner Integration Engineer

Directly sourced from GitLab's public handbook: recruiter call -> hiring-manager interview -> 2 to 5 team interviews -> a possible executive round for senior hires. No API-design-specific round or take-home is named in the posted family description; the loop reads as a fairly standard structured-interview sequence rather than a specialized work-sample-heavy one. GitLab's screening substance, per the job family's stated skills, centers on language proficiency, SDLC practice, and a real open-source contribution history that can be reviewed as existing evidence rather than tested live.

## What is confirmed vs. what is inferred

Confirmed and directly sourced: the system-design/API-design split as two distinct interview types at major companies; the PM-vs-engineer difference in what's assessed within that split; the general PM-loop take-home pattern (not platform-specific); GitLab's real posted loop structure.

Not confirmed, flagged rather than invented: a named "versioning/backward-compatibility scenario question" as a distinct, formally recognized interview format (plausible given how central this judgment is to the role, but no sourced interview account naming it as a discrete round was found); any specific SDK-design take-home account from a named company or candidate.
