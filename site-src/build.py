#!/usr/bin/env python3
"""Builds ../site from content/ using one layout. Run: npm run build"""
import datetime
import glob
import os
import re
import shutil
import subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
SITE = os.path.join(HERE, "..", "site")
BASE = "https://recscse.github.io/codive/"


def read(*parts):
    return open(os.path.join(HERE, *parts), encoding="utf8").read()


def write(rel, text):
    path = os.path.join(SITE, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf8", newline="\n") as f:
        f.write(text)


def page(rel, title, description, body, section="", image="assets/shots/changes.png"):
    root = "../" if "/" in rel else ""
    link = lambda href, label, key: (
        f'<a href="{root}{href}" class="transition hover:text-signal {"text-signal" if section == key else ""}">{label}</a>')
    return f"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{title}</title>
<meta name="description" content="{description}">
<link rel="canonical" href="{BASE}{rel}">
<meta name="theme-color" content="#fbfaf6">
<meta property="og:type" content="website">
<meta property="og:url" content="{BASE}{rel}">
<meta property="og:title" content="{title}">
<meta property="og:description" content="{description}">
<meta property="og:image" content="{BASE}{image}">
<meta name="twitter:card" content="summary_large_image">
<link rel="icon" type="image/svg+xml" href="{root}favicon.svg">
<link rel="preload" href="{root}assets/fonts/geist.woff2" as="font" type="font/woff2" crossorigin>
<link rel="stylesheet" href="{root}assets/site.css">
</head>
<body class="min-h-screen">
<header class="sticky top-0 z-40 border-b border-line bg-paper/85 backdrop-blur-lg">
  <nav class="mx-auto flex h-16 max-w-[76rem] items-center gap-8 px-6 text-[15px] text-body">
    <a href="{root}index.html" class="mr-auto text-xl font-medium tracking-[-0.03em] text-ink">codive<span class="text-signal">.</span></a>
    {link("docs.html", "Docs", "docs")}
    {link("blog.html", "Blog", "blog")}
    <a href="https://github.com/recscse/codive" class="transition hover:text-signal">GitHub</a>
    <a href="{root}docs.html#quickstart" class="hidden rounded bg-ink px-3.5 py-2 text-sm font-medium text-paper transition hover:bg-signal sm:block">Get started</a>
  </nav>
</header>
<main>
{body}
</main>
<footer class="mx-auto mt-32 flex max-w-[76rem] flex-wrap items-center gap-6 border-t border-line px-6 py-10 text-sm text-mute">
  <span class="mr-auto">codive is open source under the MIT License.</span>
  <a class="hover:text-signal" href="{root}docs.html">Docs</a>
  <a class="hover:text-signal" href="{root}blog.html">Blog</a>
  <a class="hover:text-signal" href="https://github.com/recscse/codive/releases">Releases</a>
  <a class="hover:text-signal" href="https://github.com/recscse/codive">GitHub</a>
</footer>
<script>
document.querySelectorAll('[data-copy]').forEach(function (b) {{
  b.addEventListener('click', function () {{
    navigator.clipboard.writeText(document.getElementById(b.dataset.copy).textContent).then(function () {{
      var t = b.textContent; b.textContent = 'Copied'; setTimeout(function () {{ b.textContent = t; }}, 1200);
    }});
  }});
}});
function copyCode(b) {{
  navigator.clipboard.writeText(b.closest('.codeblock').querySelector('pre').textContent).then(function () {{
    b.textContent = 'copied'; setTimeout(function () {{ b.textContent = 'copy'; }}, 1200);
  }});
}}
var links = document.querySelectorAll('.side a[href^="#"]');
if (links.length && 'IntersectionObserver' in window) {{
  var byId = {{}}; links.forEach(function (a) {{ byId[a.getAttribute('href').slice(1)] = a; }});
  var io = new IntersectionObserver(function (es) {{
    es.forEach(function (e) {{
      if (e.isIntersecting && byId[e.target.id]) {{
        links.forEach(function (a) {{ a.classList.remove('active'); }});
        byId[e.target.id].classList.add('active');
      }}
    }});
  }}, {{ rootMargin: '-10% 0px -80% 0px' }});
  Object.keys(byId).forEach(function (id) {{ var el = document.getElementById(id); if (el) io.observe(el); }});
}}
</script>
</body>
</html>
"""


def posts():
    found = []
    for path in glob.glob(os.path.join(HERE, "content", "posts", "*.html")):
        text = open(path, encoding="utf8").read()
        meta = dict(re.findall(r"<!-- (\w+): (.*?) -->", text))
        meta["body"] = re.sub(r"<!-- \w+: .*? -->\n", "", text)
        meta["slug"] = os.path.basename(path)
        meta["when"] = datetime.datetime.strptime(meta["date"], "%B %d, %Y")
        found.append(meta)
    return sorted(found, key=lambda p: (p["when"], p["slug"]), reverse=True)


def strip(html):
    return re.sub(r"<[^>]+>", "", html).replace('"', "&quot;")


def main():
    fonts = os.path.join(HERE, "node_modules", "@fontsource-variable")
    assets = os.path.join(SITE, "assets")
    os.makedirs(os.path.join(assets, "fonts"), exist_ok=True)
    shutil.copy(os.path.join(fonts, "geist", "files", "geist-latin-wght-normal.woff2"), os.path.join(assets, "fonts", "geist.woff2"))
    shutil.copy(os.path.join(fonts, "geist-mono", "files", "geist-mono-latin-wght-normal.woff2"), os.path.join(assets, "fonts", "geist-mono.woff2"))

    write("index.html", page("index.html", "codive: know what breaks before your AI agent edits your code",
                             "A local code graph and MCP server. See callers, affected files and tests for any Go change. One binary, no account.",
                             read("content", "home.html")))

    all_posts = posts()
    first, rest = all_posts[0], all_posts[1:]
    def card(p):
        return f"""<a href="blog/{p['slug']}" class="group block border-t border-line py-8">
  <p class="text-sm text-mute">{p['tag']} &nbsp;·&nbsp; {p['date']}</p>
  <h2 class="mt-3 text-2xl leading-snug font-medium tracking-[-0.02em] text-balance text-ink transition-colors group-hover:text-signal">{p['title']}</h2>
  <p class="mt-3 max-w-2xl leading-7">{p['summary']}</p>
</a>"""
    blog = f"""<div class="mx-auto max-w-[76rem] px-6 pt-24">
  <h1 class="text-7xl font-medium tracking-[-0.04em] text-ink">Blog</h1>
  <p class="mt-4 max-w-xl text-lg">How codive is built and tested, including what went wrong.</p>
  <div class="mt-16 grid gap-x-12 md:grid-cols-2">
    <div class="md:col-span-2">{card(first)}</div>
    {''.join(card(p) for p in rest)}
  </div>
</div>"""
    write("blog.html", page("blog.html", "Blog | codive", "How codive is built and tested, including what went wrong.", blog, "blog"))

    for i, p in enumerate(all_posts):
        others = [o for o in all_posts if o is not p][:2]
        more = "".join(f'<a href="{o["slug"]}" class="text-[17px] font-medium text-ink transition-colors hover:text-signal">{o["title"]}</a>' for o in others)
        body = f"""<article class="mx-auto max-w-[42rem] px-6 pt-20">
  <a href="../blog.html" class="text-sm text-mute transition hover:text-signal">&larr; Blog</a>
  <p class="mt-12 text-sm text-mute">{p['tag']} &nbsp;·&nbsp; {p['date']}</p>
  <h1 class="mt-3 text-[44px] leading-[1.08] font-medium tracking-[-0.03em] text-balance text-ink sm:text-[56px]">{p['title']}</h1>
  <div class="doc mt-10 text-[17.5px] leading-[1.75]">{p['body']}</div>
  <div class="mt-16 grid gap-3 border-t border-line pt-8 sm:grid-cols-2">{more}</div>
</article>"""
        write("blog/" + p["slug"], page("blog/" + p["slug"], strip(p["title"]) + " | codive", strip(p["summary"]), body, "blog"))

    docs = f"""<div class="mx-auto flex max-w-[76rem] gap-16 px-6">
  <aside class="side sticky top-16 hidden h-[calc(100vh-4rem)] w-56 shrink-0 overflow-y-auto py-10 lg:block">{read("content", "docs-side.html")}</aside>
  <div class="doc min-w-0 max-w-3xl flex-1 py-16">{read("content", "docs-main.html")}</div>
</div>"""
    write("docs.html", page("docs.html", "Documentation | codive", "Install codive, connect your AI tools, and use its CLI and MCP tools.", docs, "docs"))

    urls = ["", "docs.html", "blog.html"] + ["blog/" + p["slug"] for p in all_posts]
    entries = "".join(f"  <url><loc>{BASE}{u}</loc></url>\n" for u in urls)
    write("sitemap.xml", f'<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n{entries}</urlset>\n')

    cli = os.path.join(HERE, "node_modules", ".bin", "tailwindcss" + (".cmd" if os.name == "nt" else ""))
    subprocess.run([cli, "-i", os.path.join(HERE, "input.css"), "-o", os.path.join(assets, "site.css"), "--minify"], check=True, cwd=HERE, shell=os.name == "nt")
    print("built", len(all_posts), "posts")


if __name__ == "__main__":
    main()
