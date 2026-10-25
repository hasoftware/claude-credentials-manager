package shellalias

// Launcher is the command each alias body runs as `<Launcher> launch …`.
// It stays "ccm" unless cmd sets it to the name this ccm was started as,
// so a copy installed under another name (e.g. ccm2, beside upstream's
// ccm) writes aliases that call itself instead of whichever ccm comes
// first on PATH.
var Launcher = "ccm"
