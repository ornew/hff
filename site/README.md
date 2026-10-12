# HFF Site Driver

This driver runs the standard `github.com/ornew/nanosite/sitegen` pipeline.
Only the README landing page, Playground build and theme data adapter remain
project-specific; Markdown, record tables, navigation, search and publication
use the shared builder.

```sh
sh site/build.sh
sh site/build.sh -serve localhost:8080
sh hack/update-nanosite-skill.sh                 # synchronize current pin
sh hack/update-nanosite-skill.sh FULL_COMMIT_SHA # update dependency and skill
```

`site/nanosite.rev` pins a published commit; `site/go.mod` must resolve to the
same module version. The wrapper checks this before running the driver. Requires
Go 1.27.1 for the Playground; the wrapper selects this toolchain. Tests run separately: `cd site && go test ./...`.

`site/config.json` controls publication, front matter defaults, record indexes,
sections, paging, Markdown rendering, redirects and the search schema. Use the
installed `nanosite` skill and its configuration reference when editing these
settings. Existing documents retain their routes without adding front matter.

`site/public/` supplies HFF's styles and scripts; `site/layout.html` and the
landing/Playground partials retain its presentation. See
[the site guide](../docs/development/site.md) for authoring and deployment.
Do not hand-edit `site/dist`.
