This test fixture freezes the HTTP Client and Handler from commit
`2df356193294ce82673f2508a8479364061b9b08` (the genuine pre-PR implementation).
The only source adaptations are the package name, its package comment, and
removal of the unrelated inspector UI registration. Keep the old request union,
duration units, JSON names, decoding and empty-result behavior unchanged: they
are the compatibility oracle, not production routes. Tests use temporary SQLite
stores and exercise both Client/Handler upgrade orders.
