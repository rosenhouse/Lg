package extract

// PlaceFiles gives the path below extracted/ of each member of artifact.zip named in names.
func PlaceFiles(names ...string) []string {
	n := newNamer()
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i], _ = n.file(nil, name)
	}
	return paths
}
