package valueobject

import (
	"strings"
)

// RoomLabel rules. A label is a short lowercase handle a person can type to
// name a room from a chat ("/room @fox"): 1–24 characters of a–z, 0–9 and
// inner hyphens.

// NormalizeLabel canonicalizes a typed label: trimmed, lowercased, without
// a leading "@".
func NormalizeLabel(raw string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
}

// ValidLabel reports whether a normalized label is a usable handle.
func ValidLabel(label string) bool {
	if len(label) < 1 || len(label) > 24 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// animalLabels are handed to new rooms in order; the oldest rooms get the
// most memorable names.
var animalLabels = []string{
	"cat", "dog", "tiger", "lion", "wolf", "fox", "bear", "eagle", "hawk",
	"falcon", "owl", "raven", "crow", "panther", "leopard", "cheetah", "jaguar",
	"cougar", "lynx", "bobcat", "coyote", "jackal", "hyena", "otter", "seal",
	"whale", "dolphin", "orca", "shark", "ray", "turtle", "frog", "toad",
	"salamander", "newt", "gecko", "iguana", "lizard", "viper", "cobra",
	"python", "boar", "bison", "moose", "elk", "deer", "marten", "badger",
	"ferret", "weasel", "mink", "mole", "shrew", "hedgehog", "rabbit", "hare",
	"squirrel", "chipmunk", "beaver", "marmot", "hamster", "gerbil", "vole",
	"mouse", "rat", "bat", "gorilla", "chimp", "baboon", "macaque", "lemur",
	"sloth", "tapir", "rhino", "hippo", "elephant", "giraffe", "zebra", "camel",
	"alpaca", "llama", "goat", "sheep", "pony", "horse", "donkey", "mule",
	"yak", "ox", "buffalo", "kangaroo", "wombat", "koala", "panda", "platypus",
	"echidna", "possum", "armadillo", "anteater", "aardvark", "porcupine",
	"capybara", "chinchilla", "pelican", "flamingo", "penguin", "puffin",
	"heron", "crane", "stork", "egret", "ibis", "swan", "goose", "duck",
	"loon", "gull", "sparrow", "robin", "wren", "finch", "jay", "magpie",
	"dove", "emu", "kiwi", "toucan", "macaw", "parrot", "peacock", "vulture",
	"condor", "ostrich", "octopus", "squid", "crab", "shrimp", "lobster",
	"clam", "mantis", "beetle", "cricket", "hornet", "wasp", "dragonfly",
	"firefly", "scorpion",
}

// PickLabel returns the first animal not in used; once every animal is
// taken it numbers them ("cat-2"). used holds normalized labels.
func PickLabel(used map[string]bool) string {
	for _, a := range animalLabels {
		if !used[a] {
			return a
		}
	}
	for n := 2; ; n++ {
		for _, a := range animalLabels {
			l := a + "-" + itoa(n)
			if !used[l] {
				return l
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
