# Role taxonomy and ladder

Contents: the four role definitions, GitLab's real three-level ladder, and sourcing.

## API Product Manager

Owns the API as a product across its full lifecycle: strategy and vision, roadmap, development oversight (requirements, working with engineering and design), stakeholder management across engineers, marketers and sales, documentation and developer-portal input, market alignment, monetization, and iteration from usage data.

What makes this distinct from a generic PM: the "customer" is a developer integrating code against a contract, so decisions are judged on integration friction and breaking-change cost, not just feature adoption. Sourced senior/staff postings describe the scope as "develop and execute the product strategy and roadmap for API products" plus competitive analysis and monetization/pricing model design.

No standardized cross-industry IC ladder exists for this role. Progression is company-specific and generally mirrors the general product-management ladder (APM -> PM -> Senior PM -> Principal/Staff PM -> Director) with added technical/developer-facing weighting at every level.

## Platform Engineer vs. Platform Architect

Both titles are heavily overloaded across two different audiences, internal and external - see the disambiguation in the skill body before using either title in a search or a resume line. Summarized distinction where both refer to the same, external-API-facing audience:

| Aspect        | Platform Engineer                                      | Platform Architect                                        |
| ------------- | ------------------------------------------------------ | --------------------------------------------------------- |
| Primary focus | Building, operating, maintaining the platform          | Defining architectural vision and strategy                |
| Time horizon  | Day-to-day implementation                              | Long-term technical roadmap                               |
| Scope         | Specific tools, pipelines, infrastructure/API surfaces | Enterprise-wide patterns, standards, integration strategy |
| Key output    | Working, self-service platform features                | Architectural blueprints, reusable frameworks, governance |

Job postings frequently blend the two (a "Sr. Platform Engineer" posting can read as architectural), and at smaller companies one person does both; only larger orgs split them.

Stripe's own posting for **Staff Software Engineer, API Platform** is a concrete high-bar example: 12+ years technical experience, 5+ years in strategic technical leadership, "experience leading engineering team(s) working on API design, abstractions, frameworks, or client libraries," hands-on infrastructure/product delivery for internal or external customers, proficiency in Java, Ruby, Python or Go. Responsibilities are team leadership, architecture and design ownership, and roadmap partnership with engineering managers - not hands-on coding as the primary output at this level. The posting names no formal portfolio, RFC-writing, or system-design screening step explicitly.

## Partner Engineer vs. Integration Engineer

Both sit at the API-consumption boundary but differ in whether the job is relationship-facing:

- **Partner Engineer** - technical liaison between the company and external technology partners or marketplace developers. GitLab's public Partner Integration Engineering job family describes: providing consultative expertise to partners on strategic integration points using existing OAuth flows, APIs and webhooks; advocating for ISV/technology partnerships and influencing the product roadmap; leading requirements gathering; building partner tools, education and enablement. Uber frames the equivalent role as "the technical liaison between the company and external partners... to integrate technologies, products, and services into their platforms," sitting at the intersection of product, engineering, and relationship management.
- **Integration Engineer** - internal, architecture-focused: designs, builds, and supports the connections letting applications, databases, cloud services and external partners exchange data reliably (point-to-point, API-based, message queues, ESB, iPaaS patterns), owns error handling and monitoring for those integrations. The scope difference from a backend engineer: a backend engineer owns a product service; an integration engineer owns how several services and third-party systems interact at the boundary.

## GitLab's real, named ladder

GitLab publishes a real three-level ladder for the Partner Integration Engineer family - the field's one concrete, sourced example of what leveling looks like here:

- **Associate Partner Integration Engineer** - same responsibilities as the full role, less experience required.
- **Partner Integration Engineer** - full scope above; reports to the team manager.
- **Manager, Partner Integration Engineers** - owns team hiring, development and performance, sets expectations and mentors, is the subject-matter expert and cross-functional relationship owner; reports to Director, Partner Solutions Architecture.

Required skills at all levels per GitLab's posting: proficiency in Ruby/Rails or Go (or willingness to learn), modern SDLC practice (DevSecOps, CI/CD, IaC), an open-source contribution history, a bachelor's degree or equivalent, and cross-functional relationship management. GitLab also tracks explicit KPIs for this role in Salesforce: total partners supported, partner integrations launched, partner integration usage, and year-over-year partner-usage growth - a concrete, sourced example of outcome-based scorecard metrics for this role.

## Developer Platform Lead

No standardized title or ladder definition exists under this exact name. Treat it as an org-specific label for whoever owns the integration-surface strategy decisions this collection's macro strategy skills cover (which surfaces exist, the compatibility promise, partner and marketplace strategy), not as a title with its own external hiring market or published bar.

## Sources

LaunchNotes, Product Led Alliance, Axway, Chisel Labs, ProductHQ, a BuiltIn Product Manager (API) posting; Humanitec, Second Talent, BuiltIn, dev.to (Platform Engineer vs Architect); GitLab's public handbook, job family: Partner Integration Engineering (fetched directly); Uber Partner Engineer II posting (via The Muse); Stripe's careers listing for Staff Software Engineer, API Platform (fetched directly).
