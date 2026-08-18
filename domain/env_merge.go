package domain

// MergeEnv merges environment variable layers in order, with later layers
// overriding earlier ones. A nil layer is skipped.
func MergeEnv(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, layer := range layers {
		for k, v := range layer {
			out[k] = v
		}
	}
	return out
}
