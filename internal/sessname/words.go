package sessname

// The word lists below are the whole of the compressor's judgement, so they
// are deliberately small and conservative. A word wrongly listed here is a
// word that can never appear in a name, which is how two sessions end up
// sharing one.

// leadingVerbs are dropped only at the front of a title, where they say what
// is being done rather than what it is being done to. Every one of them is
// also a plausible noun somewhere else in a sentence -- port, fix, review,
// test -- which is why position decides it.
var leadingVerbs = map[string]bool{
	"add": true, "address": true, "adjust": true, "allow": true, "analyse": true,
	"analyze": true, "apply": true, "audit": true, "build": true, "check": true,
	"clean": true, "complete": true, "configure": true, "continue": true,
	"convert": true, "create": true, "debug": true, "deploy": true, "design": true,
	"diagnose": true, "document": true, "enable": true, "ensure": true,
	"examine": true, "expand": true, "explore": true, "expose": true, "extend": true,
	"extract": true, "finish": true, "fix": true, "handle": true, "harden": true,
	"implement": true, "improve": true, "integrate": true, "investigate": true,
	"land": true, "make": true, "migrate": true, "move": true, "optimise": true,
	"optimize": true, "patch": true, "plan": true, "polish": true, "port": true,
	"prepare": true, "prove": true, "publish": true, "reduce": true,
	"refactor": true, "remove": true, "rename": true, "render": true,
	"repair": true, "replace": true, "reproduce": true, "research": true,
	"resolve": true, "restore": true, "retain": true, "review": true,
	"rework": true, "ship": true, "simplify": true, "split": true, "support": true,
	"surface": true, "sync": true, "teach": true, "tidy": true, "trace": true,
	"track": true, "triage": true, "tune": true, "understand": true, "update": true,
	"upgrade": true, "validate": true, "verify": true, "wire": true, "write": true,
}

// joiners let a second leading verb be reached: "Investigate and reduce disk
// usage" is two verbs and one subject.
var joiners = map[string]bool{"and": true, "then": true, "or": true}

// stopwords carry no subject anywhere in a title.
var stopwords = map[string]bool{
	"a": true, "about": true, "across": true, "after": true, "all": true,
	"an": true, "and": true, "are": true, "as": true, "at": true, "back": true,
	"be": true, "been": true, "before": true, "being": true, "but": true,
	"by": true, "did": true, "do": true, "does": true, "done": true,
	"during": true, "each": true, "for": true, "from": true, "her": true,
	"his": true, "how": true, "i": true, "in": true, "instead": true, "into": true,
	"is": true, "it": true, "its": true, "less": true, "more": true, "most": true,
	"my": true, "new": true, "no": true, "not": true, "of": true, "old": true,
	"on": true, "onto": true, "or": true, "our": true, "over": true, "per": true,
	"so": true, "some": true, "than": true, "that": true, "the": true,
	"their": true, "then": true, "these": true, "they": true, "this": true,
	"those": true, "to": true, "under": true, "up": true, "us": true,
	"using": true, "via": true, "vs": true, "was": true, "we": true,
	"were": true, "what": true, "when": true, "where": true, "which": true,
	"while": true, "who": true, "why": true, "with": true, "without": true,
	"you": true, "your": true,
}

// genericNouns are true of almost every session on the board, so they are
// spent last. They are dropped rather than banned: a title with nothing else
// in it still gets them back.
var genericNouns = map[string]bool{
	"analysis": true, "change": true, "changes": true, "code": true,
	"detail": true, "details": true, "doc": true, "docs": true,
	"documentation": true, "feature": true, "features": true, "file": true,
	"files": true, "implementation": true, "info": true, "information": true,
	"investigation": true, "issue": true, "issues": true, "item": true,
	"items": true, "list": true, "note": true, "notes": true, "part": true,
	"plan": true, "plans": true, "problem": true, "problems": true,
	"project": true, "projects": true, "script": true, "scripts": true,
	"session": true, "sessions": true, "setup": true, "stuff": true,
	"system": true, "systems": true, "task": true, "tasks": true, "thing": true,
	"things": true, "tool": true, "tools": true, "update": true, "updates": true,
	"way": true, "ways": true, "work": true,
}
