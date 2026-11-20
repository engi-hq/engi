"""MkDocs hooks for the Engi docs site.

Each page in docs/ ends with a hand-written "← Previous | Index | Next →" footer so
the docs read well on GitHub. The site theme renders its own previous/next links,
so the footer is dropped here at build time instead of being removed from the source.
"""

import re

_FOOTER = re.compile(r"\n(?:---\s*\n\s*)?(?:\*\*← Previous\*\*|\*\*Next →\*\*)[^\n]*\s*\Z")


def on_page_markdown(markdown, **_):
    return _FOOTER.sub("\n", markdown)
