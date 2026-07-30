package zmachine

// version identifies this package's release, following semantic versioning.
//
// It is not derived from a git tag. This repository has none, so the constant
// is the only statement of the release that exists; bumping it is a deliberate
// edit, which is what TestVersion exists to enforce.
const version = "0.1.0"

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
