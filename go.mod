// No dependencies, on purpose. This binary is carried into a recovery
// environment that may have no network at all, and a module it has to fetch
// at build time is a dependency the recovery inherits.
module bmr-console

go 1.21
