"""Builds the booklet (docs/booklet/*.md) into one PDF (TODO.md Q105).

    python3 tools/booklet/booklet.py [-o kukel-booklet.pdf] [--version v1.1.0] [--html out.html]

The chapters come in the order of the booklet's README (its links to
NN-name.md files). The PDF has a title page, the README's introduction,
a contents page with page numbers, the chapters (each on a new page,
under its part), and links between chapters that stay inside the PDF;
links to the other documents of the repository point to GitHub, at the
version given. Needs python-markdown and WeasyPrint
(tools/booklet/requirements.txt); make booklet installs them in a
virtual environment of its own.
"""

import argparse
import datetime
import html
import os
import re
import sys

import markdown
import weasyprint

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.normpath(os.path.join(HERE, "..", ".."))
BOOKLET = os.path.join(ROOT, "docs", "booklet")
REPO = "https://github.com/maarten-boot/kukel"

# The parts, by the number of their first chapter.
PARTS = {1: "Part I: the language", 13: "Part II: the modules", 22: "Appendices"}

CSS = """
@page {
    size: A4;
    margin: 22mm 20mm 24mm 20mm;
    @bottom-center { content: counter(page); font-size: 9pt; color: #333; }
    @top-right { content: string(chapter); font-size: 8.5pt; color: #333; }
}
@page :first { @bottom-center { content: none; } @top-right { content: none; } }
@page front { @bottom-center { content: none; } @top-right { content: none; } }
@page part { @top-right { content: none; } }
html { font-family: "DejaVu Serif", "Noto Serif", serif; font-size: 10.5pt; line-height: 1.45; color: #000; background: #fff; }
h1, h2, h3, .part { font-family: "DejaVu Sans", "Noto Sans", sans-serif; color: #000; }
h1 { font-size: 20pt; margin: 0 0 12pt 0; string-set: chapter content(); break-before: page; }
h2 { font-size: 13.5pt; margin: 18pt 0 6pt 0; break-after: avoid; }
h3 { font-size: 11.5pt; break-after: avoid; }
p, li { orphans: 3; widows: 3; }
a { color: #0b3d91; text-decoration: none; }
code, pre { font-family: "DejaVu Sans Mono", "Noto Sans Mono", monospace; }
code { font-size: 9pt; }
pre { font-size: 8.4pt; line-height: 1.35; background: #fcfcfc; color: #000; border: 0.6pt solid #999;
      padding: 6pt 8pt; white-space: pre-wrap; overflow-wrap: anywhere; break-inside: avoid; }
p + pre { break-before: avoid; }
pre.long { break-inside: auto; }
pre code { font-size: inherit; }
table { border-collapse: collapse; margin: 8pt 0; font-size: 9.2pt; break-inside: auto; }
th, td { border: 0.5pt solid #888; padding: 3pt 5pt; vertical-align: top; text-align: left; }
th { background: #e8e8e8; }
img { max-width: 100%; }
.front { page: front; text-align: center; }
.front .title { font-family: "DejaVu Sans", sans-serif; font-size: 30pt; font-weight: bold; margin-top: 18mm; }
.front .sub { font-size: 13pt; margin-top: 6pt; }
.front .meta { font-size: 10pt; margin-top: 40mm; color: #222; }
.part { page: part; break-before: page; font-size: 24pt; font-weight: bold; text-align: center; margin-top: 80mm; }
.intro h1 { break-before: page; }
.toc h1 { break-before: page; }
.toc ul { list-style: none; padding-left: 0; }
.toc li.part-entry { font-weight: bold; margin-top: 8pt; }
.toc li a::after { content: leader(".") target-counter(attr(href), page); }
.toc li.part-entry a::after { content: none; }
"""


def chapters():
    """The chapter files, in the order the booklet's README links them."""
    readme = open(os.path.join(BOOKLET, "README.md"), encoding="utf-8").read()
    seen, files = set(), []
    for name in re.findall(r"\]\((\d\d-[a-z0-9-]+\.md)\)", readme):
        if name not in seen:
            seen.add(name)
            files.append(name)
    return files


def blob(path, version):
    """The GitHub address of a file of the repository: at the release's tag
    for a release (v1.2.3), else on main."""
    ref = version if re.match(r"^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$", version) else "main"
    return "%s/blob/%s/%s" % (REPO, ref, path)


def convert(text, chapter, names, version, source_dir):
    """A chapter's Markdown as HTML: headings get ids prefixed with the
    chapter, links to chapters become links in the PDF, links to other
    files of the repository go to GitHub, and pictures are found on disk."""
    # The navigation lines at the top and bottom of each chapter.
    text = "\n".join(l for l in text.split("\n") if not l.startswith("[The booklet](README.md)"))

    def slugify(value, separator):
        slug = re.sub(r"[^\w\s-]", "", value).strip().lower()
        return chapter + "--" + re.sub(r"[\s]+", separator, slug)

    md = markdown.Markdown(extensions=["fenced_code", "tables", "toc"],
                           extension_configs={"toc": {"slugify": slugify}})
    body = md.convert(text)

    def link(m):
        href = html.unescape(m.group(1))
        if re.match(r"^[a-z]+:", href) or href.startswith("#"):
            if href.startswith("#"):
                return 'href="#%s--%s"' % (chapter, href[1:])
            return m.group(0)
        path, _, frag = href.partition("#")
        if path in names:
            target = path[:-3]
            return 'href="#%s"' % (target + ("--" + frag if frag else ""))
        if path == "README.md":
            return 'href="#contents"'
        repo_path = os.path.relpath(os.path.normpath(os.path.join(source_dir, path)), ROOT)
        return 'href="%s"' % html.escape(blob(repo_path, version) + ("#" + frag if frag else ""))

    body = re.sub(r'href="([^"]*)"', link, body)

    def image(m):
        src = html.unescape(m.group(1))
        if re.match(r"^[a-z]+:", src):
            return m.group(0)
        return 'src="file://%s"' % html.escape(os.path.normpath(os.path.join(source_dir, src)))

    body = re.sub(r'src="([^"]*)"', image, body)
    # Long code blocks may break across pages; short ones stay whole.
    body = re.sub(r"<pre>(<code[^>]*>)((?:.|\n)*?)</pre>",
                  lambda m: ('<pre class="long">' if m.group(2).count("\n") > 40 else "<pre>") + m.group(1) + m.group(2) + "</pre>",
                  body)
    return body


def build(version):
    names = chapters()
    if not names:
        sys.exit("booklet: no chapters found in docs/booklet/README.md")
    readme = open(os.path.join(BOOKLET, "README.md"), encoding="utf-8").read()
    # The introduction: the README up to its first list of chapters, without
    # its title (the title page has it).
    intro = readme.split("\n## ", 1)[0]
    intro = re.sub(r"^# .*\n", "", intro)
    intro = re.sub(r'<p align="center"><img src="\.\./Kukel-text-small\.png"[^\n]*\n', "", intro)   # the title page has the logo
    intro = intro.replace("Every program in the booklet", "Every program in this booklet")
    parts = []
    logo = os.path.join(ROOT, "docs", "Kukel-text-small.png")
    today = datetime.date.today().isoformat()
    parts.append('<section class="front"><img src="file://%s" style="width: 38%%">'
                 '<div class="title">The Kukel booklet</div>'
                 '<div class="sub">a small language, from the first program to windows and networks</div>'
                 '<div class="meta">kukel %s · %s<br>%s</div></section>'
                 % (html.escape(logo), html.escape(version), today, html.escape(REPO)))
    parts.append('<section class="intro"><h1 id="about">About this booklet</h1>%s</section>'
                 % convert(intro, "about", names, version, BOOKLET))

    toc, body = [], []
    titles = {}
    for name in names:
        text = open(os.path.join(BOOKLET, name), encoding="utf-8").read()
        title = re.search(r"^# (.*)$", text, re.M).group(1)
        titles[name] = title
        number = int(name[:2])
        if number in PARTS:
            pid = "part-%d" % number
            body.append('<div class="part" id="%s">%s</div>' % (pid, html.escape(PARTS[number])))
            toc.append('<li class="part-entry"><a href="#%s">%s</a></li>' % (pid, html.escape(PARTS[number])))
        chapter = name[:-3]
        html_text = convert(text, chapter, names, version, BOOKLET)
        # The chapter's own id on its heading, for links to the chapter.
        html_text = re.sub(r"<h1 id=\"[^\"]*\">", '<h1 id="%s">' % chapter, html_text, count=1)
        body.append("<section>%s</section>" % html_text)
        toc.append('<li><a href="#%s">%s</a></li>' % (chapter, html.escape(title)))
    parts.append('<section class="toc"><h1 id="contents">Contents</h1><ul>%s</ul></section>' % "".join(toc))
    parts.extend(body)
    return ('<!doctype html><html lang="en"><head><meta charset="utf-8"><title>The Kukel booklet</title>'
            '<style>%s</style></head><body>%s</body></html>' % (CSS, "\n".join(parts)))


def main():
    ap = argparse.ArgumentParser(description="Build the Kukel booklet as one PDF.")
    ap.add_argument("-o", "--output", default="kukel-booklet.pdf")
    ap.add_argument("--version", default="main", help="the version on the title page, and of the GitHub links")
    ap.add_argument("--html", help="also write the HTML the PDF is made from")
    args = ap.parse_args()
    doc = build(args.version)
    if args.html:
        open(args.html, "w", encoding="utf-8").write(doc)
    weasyprint.HTML(string=doc, base_url=BOOKLET).write_pdf(args.output)
    print(args.output)


if __name__ == "__main__":
    main()
