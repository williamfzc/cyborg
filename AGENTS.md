# AGENTS.md
Before you write a single line in my repository, read this.
It is not a style guide. It is the shape of mind I want you to wear while you work here.
Everything here is in English, because the work is in English.
---
## Before you act
Add nothing that is not needed now.
The future will make its own demands; it does not need your sympathy.
Before creating a file, a layer, a flag, or an abstraction, ask whether something
that already exists can carry the weight. If it can, let it.
Before writing any fact, search for it.
A fact — an interface, a default, a decision, a responsibility — lives in exactly
one place. Everything else links to it. Two notes that disagree are not two
opinions; they are one bug. When you cannot reconcile them in the moment, name
the conflict openly. Silence is how contradictions survive.
---
## While you write
Write so the codebase reads like a well-indexed book.
Every source file opens with a short header: what it is, and where it stands in
the whole. A file whose role cannot be stated in two lines does not yet know
what it is.
One language, one voice. Code, identifiers, comments, commits, docs — all English.
---
## When you finish
Leave the docs truer than you found them.
I keep a `docs/` tree alongside the code, maintained as an Obsidian vault:
nested by concern, linked by `[[wiki-links]]`, mapped by per-directory indexes,
atomic in its notes. The graph is the architecture, made visible.
Refresh it whenever a unit of work closes — before the commit, after the commit,
at the end of the session. Drift is decay.
Let your commits read as narrative. I follow Angular Conventional Commits as
the grammar of that narrative — not for tooling, but for the discipline of
naming each move. A commit that cannot be summarized in one honest line is
probably two commits.
---
## What I refuse
Abstractions without a second caller.
Junk-drawer modules with no thesis.
Parallel structures that never deprecate their predecessor.
Code that moves while its header or its doc stays behind.
The same truth stated twice instead of linked once.
These are not style preferences. They are failures of the principles above.
When you see them — mine or yours — name them.
---
## How I want you to work with me
Tell me when I violate these principles. Do not sink quietly into my mistakes.
When a live instruction of mine contradicts this text, follow the instruction,
but flag the contradiction so this text can grow.
When you are uncertain, prefer to do less. Do not fill uncertainty with motion.
---
When in doubt, choose the version with fewer things in it.
When certain, check once more.
