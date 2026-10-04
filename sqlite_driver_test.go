package comitia

// Internal storage tests retain access to SQL implementation details without
// introducing a production import from comitia to its sqlite subpackage.
import _ "modernc.org/sqlite"
