package zmachine

// version identifies this package's release, following semantic versioning.
//
// It is not derived from a git tag. The constant is the only statement of the
// release in the source and the tag is the only one Go's module resolution can
// see, so the two are maintained together: bumping the constant is a deliberate
// edit, which is what TestVersion exists to enforce, and the commit that bumps
// it is tagged to match.
const version = "0.2.0"

// Version returns the semantic version of this package.
//
// It is here for a host that reports which engine it is running to its
// operators - the web server this package is embedded in prints it beside its
// own build - and that is the whole of what it is for.
//
// It reports nothing about conformance, deliberately. This engine implements
// Version 3 of the Z-machine and is written against Standard 1.1 in
// references/z-spec11, but the interoperability testing that would justify
// claiming that standard is not far enough along, and the engine correspondingly
// leaves the header's standard revision number ($32-$33, S 11.1) unset rather
// than filling it in. Which build a host is running and which standard an engine
// meets are two different statements, and only the first is made here.
func Version() string {
	return version
}
