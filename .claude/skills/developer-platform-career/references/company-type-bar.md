# Company type bar

Contents: the three company archetypes, the dominant real-world condition that most API teams are small, and what a candidate should expect at each.

## The three company archetypes

- **Infrastructure/API-first companies** (Stripe, Twilio-style) - the API _is_ the product. Stripe's own Staff Software Engineer, API Platform posting explicitly invites candidates "whether they've spent their entire career in infrastructure or are looking to apply their skills... for the first time," framing the work as building "high-performance infrastructure products for both internal and external customers." Bar: highest technical depth, dedicated platform teams, staff-level roles carrying 12+ years experience and 5+ years of strategic technical leadership as a stated minimum.
- **Companies bolting a public API onto an existing product** - no single sourced example was directly confirmed, but the shape is implied by the general prevalence of small API teams (below) and by job postings that ask for "API design and development" as one line item inside an otherwise general backend-engineering role rather than as a dedicated platform team's sole mandate. Treat this archetype as evidenced by absence - the specialization is thinner and folded into general backend hiring - rather than by a named case study.
- **Enterprise platforms with large partner/ISV ecosystems** - evidenced by GitLab's dedicated, three-level Partner Integration Engineering job family reporting up through a Director, Partner Solutions Architecture, with tracked KPIs (partners supported, integrations launched, integration usage, year-over-year growth) - a structurally different hiring pattern because the deliverable is partner-relationship-mediated, not a solo technical build.

## The dominant real-world condition: most API teams are small

Postman's State of the API Report found **84% of API teams operate in groups of 1-9 people**, with API-related work spread across testing (81%), development/implementation (73%), and documentation (58%) inside that small group - and no distinct, centralized "platform product manager" or "platform engineering" function called out separately from general engineering/software-development roles (73% of respondents identified as working in engineering/software development overall). Ownership of API strategy is described as distributed rather than centralized even in "API-first" organizations (82% of organizations report an API-first approach, but only 25% describe themselves as fully API-first).

**This is the single most important company-type finding for a candidate:** a dedicated, specialized developer-platform hire (API PM, platform architect, and partner engineer as three separate people) is the exception, not the norm. At the modal company, one or two people on a small team absorb API design, partner integration, and often some developer-facing communication simultaneously.

## What differs across the three archetypes

| Dimension                      | Infra/API-first                                                                               | Bolt-on public API                                                                                          | Enterprise partner ecosystem                                                                                |
| ------------------------------ | --------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| Team dedicated to the platform | Yes, often multiple specialized teams                                                         | Rarely a dedicated team; folded into general backend engineering                                            | Yes, a named partner-engineering function with its own manager and director                                 |
| What a hire is measured on     | API design quality, infra reliability, developer-facing contract stability                    | General engineering delivery, API design as one competency among several                                    | Named partner-facing KPIs: partners supported, integrations shipped, usage growth                           |
| Portfolio bar for a candidate  | Highest - a staff-level posting can expect 12+ years and prior leadership of API-design teams | Lower, folded into a general "API design and development" line item                                         | Relationship plus technical hybrid: open-source contribution history, partner-facing communication          |
| Risk when evaluating an offer  | Scope is real and specialized, but competition for the role is high                           | The role may be under-titled or under-leveled relative to actual scope, or pulled into general backend work | The role is measured on partner metrics the candidate can't fully control (the partner's own adoption pace) |

## What this means for a candidate

Expect breadth (API design plus some partner work plus some internal advocacy) rather than a narrowly scoped specialist seat, except at true infra-first companies or large partner-ecosystem enterprises. Asking which archetype a target company is - dedicated platform org, general engineering team adding an API line item, or a named partner-ecosystem function - before accepting an offer sized for a specialist role is the single most useful question this file supports.
