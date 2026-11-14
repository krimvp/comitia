The inspector uses the landing page's drafting palette and IBM Plex Sans/Mono,
with system light/dark preference. Typography is self-hosted: the four unmodified
Latin1 WOFF2 files from `site/assets/fonts` and their SIL OFL license are embedded
in the Go binary and served only from `/ui/`. No font CDN is used.
System font fallbacks cover characters outside the Latin1 subset.

The shared mark and connection-form crop corners provide identity; the working
views retain their compact layout, pagination and render limits. Text tokens are
checked at WCAG AA contrast in both themes, with separate high-contrast control
borders and sidebar colors. Browser checks verify actual local font loads and
computed text contrast on conversation, diagram and control surfaces.

Fonts are IBM's unmodified Latin1 splits from npm `@ibm/plex-sans` 1.1.0 and
`@ibm/plex-mono` 2.5.0, under SIL Open Font License 1.1 (see `OFL.txt`).
The flat asset paths also let the historical CLI compatibility harness copy the
inspector's complete embedded asset set.
