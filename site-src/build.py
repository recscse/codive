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
        f'<a href="{root}{href}" class="transition hover:text-white {"text-white" if section == key else ""}">{label}</a>')
    return f"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{title}</title>
<meta name="description" content="{description}">
<link rel="canonical" href="{BASE}{rel}">
<meta name="theme-color" content="#08090b">
<meta property="og:type" content="website">
<meta property="og:url" content="{BASE}{rel}">
<meta property="og:title" content="{title}">
<meta property="og:description" content="{description}">
<meta property="og:image" content="{BASE}{image}">
<meta name="twitter:card" content="summary_large_image">
<link rel="icon" type="image/svg+xml" href="{root}favicon.svg">
<link rel="preload" href="{root}assets/fonts/inter.woff2" as="font" type="font/woff2" crossorigin>
<link rel="stylesheet" href="{root}assets/site.css">
</head>
<body class="min-h-screen bg-ink">
<header class="sticky top-0 z-40 border-b border-white/[.06] bg-ink/75 backdrop-blur-xl">
  <nav class="mx-auto flex h-14 max-w-[88rem] items-center gap-7 px-6 text-sm text-zinc-400">
    <a href="{root}index.html" class="mr-auto text-[15px] font-semibold tracking-tight text-white">codive</a>
    {link("docs.html", "Docs", "docs")}
    {link("blog.html", "Blog", "blog")}
    <a href="https://github.com/recscse/codive" class="transition hover:text-white">GitHub</a>
    <a href="{root}docs.html#quickstart" class="hidden rounded-lg bg-white px-3 py-1.5 text-[13px] font-medium text-black transition hover:bg-zinc-200 sm:block">Get started</a>
  </nav>
</header>
<main>
{body}
</main>
<footer class="mx-auto mt-28 flex max-w-[88rem] flex-wrap items-center gap-6 border-t border-edge px-6 py-8 text-[13px] text-zinc-500">
  <span class="mr-auto">codive is open source under the MIT License.</span>
  <a class="hover:text-white" href="{root}docs.html">Docs</a>
  <a class="hover:text-white" href="{root}blog.html">Blog</a>
  <a class="hover:text-white" href="https://github.com/recscse/codive/releases">Releases</a>
  <a class="hover:text-white" href="https://github.com/recscse/codive">GitHub</a>
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
    shutil.copy(os.path.join(fonts, "inter", "files", "inter-latin-wght-normal.woff2"), os.path.join(assets, "fonts", "inter.woff2"))
    shutil.copy(os.path.join(fonts, "jetbrains-mono", "files", "jetbrains-mono-latin-wght-normal.woff2"), os.path.join(assets, "fonts", "jetbrains-mono.woff2"))

    write("index.html", page("index.html", "codive: know what breaks before your AI agent edits your code",
                             "A local code graph and MCP server. See callers, affected files and tests for any Go change. One binary, no account.",
                             read("content", "home.html")))

    all_posts = posts()
    first, rest = all_posts[0], all_posts[1:]
    def card(p):
        return f"""<a href="blog/{p['slug']}" class="group flex flex-col rounded-2xl border border-edge bg-surface p-7 transition hover:border-zinc-600">
  <p class="text-xs text-zinc-500">{p['tag']} &nbsp;·&nbsp; {p['date']}</p>
  <h2 class="mt-3 text-xl leading-snug font-semibold tracking-tight text-balance text-white">{p['title']}</h2>
  <p class="mt-3 text-[14.5px] leading-6">{p['summary']}</p>
</a>"""
    blog = f"""<div class="mx-auto max-w-5xl px-6 pt-20">
  <h1 class="text-5xl font-semibold tracking-tight text-white">Blog</h1>
  <p class="mt-4 max-w-xl text-lg">How codive is built and tested, including what went wrong.</p>
  <div class="mt-12 grid gap-4 md:grid-cols-2">
    <div class="md:col-span-2">{card(first)}</div>
    {''.join(card(p) for p in rest)}
  </div>
</div>"""
    write("blog.html", page("blog.html", "Blog | codive", "How codive is built and tested, including what went wrong.", blog, "blog"))

    for i, p in enumerate(all_posts):
        others = [o for o in all_posts if o is not p][:2]
        more = "".join(f'<a href="{o["slug"]}" class="rounded-xl border border-edge bg-surface p-5 text-[14.5px] font-medium text-zinc-200 transition hover:border-zinc-600">{o["title"]}</a>' for o in others)
        body = f"""<article class="mx-auto max-w-[44rem] px-6 pt-16">
  <a href="../blog.html" class="text-sm text-zinc-500 transition hover:text-white">&larr; Blog</a>
  <p class="mt-10 text-sm text-zinc-500">{p['tag']} &nbsp;·&nbsp; {p['date']}</p>
  <h1 class="mt-3 text-4xl leading-tight font-semibold tracking-tight text-balance text-white sm:text-[44px]">{p['title']}</h1>
  <div class="doc mt-8 text-[16.5px] leading-8">{p['body']}</div>
  <div class="mt-16 grid gap-3 border-t border-edge pt-8 sm:grid-cols-2">{more}</div>
</article>"""
        write("blog/" + p["slug"], page("blog/" + p["slug"], strip(p["title"]) + " | codive", strip(p["summary"]), body, "blog"))

    docs = f"""<div class="mx-auto flex max-w-[88rem] gap-12 px-6">
  <aside class="side sticky top-14 hidden h-[calc(100vh-3.5rem)] w-60 shrink-0 overflow-y-auto py-10 lg:block">{read("content", "docs-side.html")}</aside>
  <div class="doc min-w-0 max-w-3xl flex-1 py-12">{read("content", "docs-main.html")}</div>
</div>"""
    write("docs.html", page("docs.html", "Documentation | codive", "Install codive, connect your AI tools, and use its CLI and MCP tools.", docs, "docs"))

    urls = ["", "docs.html", "blog.html"] + ["blog/" + p["slug"] for p in all_posts]
    entries = "".join(f"  <url><loc>{BASE}{u}</loc></url>\n" for u in urls)
    write("sitemap.xml", f'<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n{entries}</urlset>\n')

    shutil.copytree(os.path.join(HERE, "fonts-css"), assets, dirs_exist_ok=True) if os.path.isdir(os.path.join(HERE, "fonts-css")) else None
    cli = os.path.join(HERE, "node_modules", ".bin", "tailwindcss" + (".cmd" if os.name == "nt" else ""))
    subprocess.run([cli, "-i", os.path.join(HERE, "input.css"), "-o", os.path.join(assets, "site.css"), "--minify"], check=True, cwd=HERE, shell=os.name == "nt")
    print("built", len(all_posts), "posts")


if __name__ == "__main__":
    main()
