package session

import (
	"crypto/sha256"
	"encoding/hex"
)

// Keep this list unchanged: its order and length determine existing session handles.
var animals = [...]string{
	"fox", "bird", "bear", "wolf", "frog", "deer", "duck", "goat", "hare", "hawk",
	"lion", "lynx", "mole", "moth", "puma", "seal", "swan", "toad", "wasp", "boar",
	"crab", "crow", "dove", "fish", "goose", "gull", "ibis", "koala", "lemur", "llama",
	"moose", "mouse", "newt", "okapi", "otter", "panda", "pike", "quail", "quokka", "raven",
	"shark", "sheep", "skunk", "snail", "snake", "squid", "stork", "tapir", "tiger", "trout",
	"tuna", "turkey", "viper", "whale", "zebra", "eagle", "finch", "gecko", "heron", "horse",
}

// words returns stable animal words for a native session and its collision fallback.
func words(agent, id string) []string {
	sum := sha256.Sum256([]byte(agent + "\x00" + id))
	words := make([]string, len(sum))
	for i, b := range sum {
		words[i] = animals[int(b)%len(animals)]
	}
	return words
}

// Name gives a native session a stable animal handle.
func Name(agent, id string) string {
	return words(agent, id)[0]
}

// previousName preserves the earlier six-character alphanumeric handles.
func previousName(agent, id string) string {
	shortAnimals := [...]string{"ant", "bat", "bee", "cat", "cod", "cow", "dog", "eel", "elk", "emu", "fox", "gnu", "hen", "hog", "jay", "koi", "owl", "pig", "pug", "ram", "rat", "yak"}
	const alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
	sum := sha256.Sum256([]byte(agent + "\x00" + id))
	return shortAnimals[int(sum[0])%len(shortAnimals)] + string([]byte{
		alphabet[int(sum[1])%len(alphabet)],
		alphabet[int(sum[2])%len(alphabet)],
		alphabet[int(sum[3])%len(alphabet)],
	})
}

// legacyName preserves handles produced before short animal names.
func legacyName(agent, id string) string {
	dogs := [...]string{"akita", "beagle", "collie", "corgi", "dingo", "finn", "husky", "jindo", "labrador", "milo", "poodle", "pug", "retriever", "shiba", "terrier", "whippet"}
	sum := sha256.Sum256([]byte(agent + "\x00" + id))
	return dogs[int(sum[0])%len(dogs)] + "-" + hex.EncodeToString(sum[1:5])
}
