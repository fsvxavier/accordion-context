# Compensation

Contents: why no survey-grade dataset exists, the one structural finding worth stating explicitly, and what to check for a live figure.

## No survey-grade dataset exists for this niche

Direct lookups (Levels.fyi, Glassdoor, Payscale, BuiltIn) either 404'd, returned no usable content, or blocked scraping - consistent with DevRel's own compensation gap. No dated, named-source figure for "API Product Manager" or "Platform Engineer" total comp should be presented as verified without a fresh, sourced check. State this gap plainly rather than inventing a number.

## The key structural finding: platform comp rides the general ladder, unlike DevRel

This is the one finding worth stating explicitly and prominently, because it flips how a candidate should approach benchmarking relative to the DevRel sibling track:

- Stripe's own posting for this exact function is titled **"Staff Software Engineer, API Platform"** - the leveling word is "Staff Software Engineer," the general engineering ladder rung, with "API Platform" as a qualifier/team name, not a separate ladder. This is a directly sourced, concrete data point that at least one major infra-first company places platform engineering on its general software-engineering levels rather than carving out a bespoke platform-specific pay scale.
- The API Product Manager literature converges on the same pattern from the PM side: career-progression sources describe API PM levels as "typically company-specific rather than standardized industry-wide, though it generally mirrors general product management career ladders with added emphasis on technical/developer-facing skills at every level" - not a separate track, just a general PM ladder with a specialization layered on top.
- This contrasts with DevRel, where compensation has no reliable anchor to an existing ladder at small or mid companies (only large companies level advocates on the engineering scale), which is why DevRel pay is comparatively undersurveyed and unpredictable across company sizes.

**Practical implication:** where a DevRel candidate must go find a dated crowdsourced figure and treat it as directional because no ladder anchors it, a developer-platform candidate can instead benchmark this role against the target company's general software-engineering (or general product-management) comp bands at the equivalent level - the same way any other backend-engineering or PM specialization gets benchmarked. This is a materially more actionable answer, and should be stated as a key differentiator from the DevRel sibling track rather than hedged away.

## What to check for a live figure

- The company's own published engineering or PM level ladder and comp bands, if public (GitLab publishes both, though the specific compensation figures for its Partner Integration Engineer family were not visible in the fetched handbook page - only the job-level structure was).
- Crowdsourced leveling sites (Levels.fyi) searched by the _general_ engineering or PM title plus team name (for example, "Staff Software Engineer" at the target company, filtered to a platform or API team) rather than by a bespoke "platform engineer" or "API product manager" title, since those bespoke titles return sparse or no data.
- Devtools-focused recruiters, the same channel DevRel names for its own undersurveyed compensation gap.

Never quote an undated figure from memory; if you can browse, pull a current figure with its source, date, and level before using it in any deliverable.
