# Parser Generation and Maintenance

`internal/syntax/hff.pego` is the authoritative HFF grammar. The generated
`internal/syntax/parser_gen.go` contains the standalone typed parser and must not
be edited by hand. It has no runtime dependency on the PEGO module.

Use Go 1.27.1 and regenerate from the repository root:

```sh
sh hack/generate-parser.sh
```

The script invokes PEGO at the pinned revision
[ee3b6d2344d074504aa198d08b591d9788c75ecf](https://github.com/ornew/pego/commit/ee3b6d2344d074504aa198d08b591d9788c75ecf).
If a local PEGO executable is available, set `PEGO_BIN` to its path; the script
then uses that executable instead of resolving the pinned Go module version.
The local binary should be built from the pinned revision to keep output
reproducible. The neighboring `../pego` checkout is a read-only reference.

After grammar changes, regenerate the parser and inspect both the grammar and
generated diff. Keep parser actions limited to building syntax nodes; schema
resolution, type checking, capability checks, and filter validation belong to
common compilation. Source spans use UTF-8 byte offsets.

`syntax.ParseQuery` applies query-byte and syntax-depth limits before and after
generated parsing. Depth starts at zero for the root and excludes singleton
Or/And wrappers and Access nodes without suffixes. It includes groups, operators,
containers, guards, access suffixes, and callback bodies. The preflight scan
bounds recursive syntax before calling the generated parser. Context cancellation
is checked before and after generated parsing and during preflight/tree
validation; `ParseAST` itself has no context hook. Preserve the generated
parser's 100,000 rule-call safety limit.
