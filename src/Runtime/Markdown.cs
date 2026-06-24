// Markdown - a tiny, dependency-free markdown parser. It does NOT render; it turns markdown text into a
// flat list of typed lines (each with inline spans) a caller can map to its own formatting (the console
// uses StripFence to unwrap code fences). Keeping the parse here (no UI deps) means the console build's
// SelfTest can cover it. Supported: fenced/indented code blocks, headings, unordered/ordered lists,
// blockquotes, horizontal rules, and inline code / bold / italic / links. This is a pragmatic subset,
// not a CommonMark implementation.

using System;
using System.Collections.Generic;
using System.Text;

namespace Icm
{
    internal enum MdLineKind { Blank, Paragraph, Heading, Bullet, Ordered, Code, Rule, Quote }

    internal enum MdSpanStyle { Plain, Bold, Italic, Code, Link }

    internal class MdSpan
    {
        public string Text;
        public MdSpanStyle Style;
        public string Href;     // for Link
        public MdSpan(string text, MdSpanStyle style) { Text = text; Style = style; }
    }

    internal class MdLine
    {
        public MdLineKind Kind;
        public int Level;               // heading level (1-6), or list indent depth
        public string Marker;           // ordered-list number text, e.g. "2"
        public string Raw;              // verbatim text for Code lines
        public List<MdSpan> Spans = new List<MdSpan>();
    }

    internal static class Markdown
    {
        public static List<MdLine> Parse(string md)
        {
            var lines = new List<MdLine>();
            if (md == null) md = "";
            string[] raw = md.Replace("\r\n", "\n").Replace("\r", "\n").Split('\n');
            bool inFence = false;

            foreach (string line in raw)
            {
                if (line.TrimStart().StartsWith("```"))
                {
                    inFence = !inFence;     // the fence line itself is not emitted
                    continue;
                }
                if (inFence)
                {
                    lines.Add(new MdLine { Kind = MdLineKind.Code, Raw = line });
                    continue;
                }

                string s = line;
                if (s.Trim().Length == 0) { lines.Add(new MdLine { Kind = MdLineKind.Blank }); continue; }

                int h = HeadingLevel(s);
                if (h > 0)
                {
                    var hl = new MdLine { Kind = MdLineKind.Heading, Level = h };
                    hl.Spans = ParseInline(s.Substring(h).Trim());
                    lines.Add(hl);
                    continue;
                }

                string st = s.Trim();
                if (st == "---" || st == "***" || st == "___") { lines.Add(new MdLine { Kind = MdLineKind.Rule }); continue; }

                if (st.StartsWith("> "))
                {
                    var q = new MdLine { Kind = MdLineKind.Quote };
                    q.Spans = ParseInline(st.Substring(2));
                    lines.Add(q);
                    continue;
                }

                int indent; string marker; string rest;
                if (TryBullet(s, out indent, out rest))
                {
                    var b = new MdLine { Kind = MdLineKind.Bullet, Level = indent };
                    b.Spans = ParseInline(rest);
                    lines.Add(b);
                    continue;
                }
                if (TryOrdered(s, out indent, out marker, out rest))
                {
                    var o = new MdLine { Kind = MdLineKind.Ordered, Level = indent, Marker = marker };
                    o.Spans = ParseInline(rest);
                    lines.Add(o);
                    continue;
                }

                var p = new MdLine { Kind = MdLineKind.Paragraph };
                p.Spans = ParseInline(s.TrimEnd());
                lines.Add(p);
            }
            return lines;
        }

        // Split a line into inline spans: `code`, **bold**, *italic* / _italic_, [text](url), plain.
        public static List<MdSpan> ParseInline(string text)
        {
            var spans = new List<MdSpan>();
            if (string.IsNullOrEmpty(text)) return spans;
            var plain = new StringBuilder();
            int i = 0, n = text.Length;
            while (i < n)
            {
                char ch = text[i];

                if (ch == '`')
                {
                    int e = text.IndexOf('`', i + 1);
                    if (e > i) { Flush(spans, plain); spans.Add(new MdSpan(text.Substring(i + 1, e - i - 1), MdSpanStyle.Code)); i = e + 1; continue; }
                }
                if (ch == '*' && i + 1 < n && text[i + 1] == '*')
                {
                    int e = text.IndexOf("**", i + 2, StringComparison.Ordinal);
                    if (e > i + 1) { Flush(spans, plain); spans.Add(new MdSpan(text.Substring(i + 2, e - i - 2), MdSpanStyle.Bold)); i = e + 2; continue; }
                }
                if (ch == '*' || ch == '_')
                {
                    int e = text.IndexOf(ch, i + 1);
                    if (e > i + 1) { Flush(spans, plain); spans.Add(new MdSpan(text.Substring(i + 1, e - i - 1), MdSpanStyle.Italic)); i = e + 1; continue; }
                }
                if (ch == '[')
                {
                    int c = text.IndexOf(']', i + 1);
                    if (c > i && c + 1 < n && text[c + 1] == '(')
                    {
                        int p = text.IndexOf(')', c + 2);
                        if (p > c)
                        {
                            Flush(spans, plain);
                            var span = new MdSpan(text.Substring(i + 1, c - i - 1), MdSpanStyle.Link);
                            span.Href = text.Substring(c + 2, p - c - 2);
                            spans.Add(span);
                            i = p + 1; continue;
                        }
                    }
                }

                plain.Append(ch);
                i++;
            }
            Flush(spans, plain);
            return spans;
        }

        // If the text is a single fenced code block (```lang ... ```), return just the inner code;
        // otherwise return it unchanged. Used when writing generated code to a file - the model often
        // wraps it in a fence despite being told not to.
        public static string StripFence(string text)
        {
            if (string.IsNullOrEmpty(text)) return text;
            string t = text.Trim();
            if (!t.StartsWith("```")) return text;
            int firstNl = t.IndexOf('\n');
            if (firstNl < 0) return text;
            int lastFence = t.LastIndexOf("```", StringComparison.Ordinal);
            if (lastFence <= firstNl) return text;
            return t.Substring(firstNl + 1, lastFence - firstNl - 1).TrimEnd('\r', '\n');
        }

        private static void Flush(List<MdSpan> spans, StringBuilder plain)
        {
            if (plain.Length > 0) { spans.Add(new MdSpan(plain.ToString(), MdSpanStyle.Plain)); plain.Length = 0; }
        }

        private static int HeadingLevel(string s)
        {
            int n = 0;
            while (n < s.Length && s[n] == '#') n++;
            if (n >= 1 && n <= 6 && n < s.Length && s[n] == ' ') return n;
            return 0;
        }

        private static bool TryBullet(string line, out int indent, out string rest)
        {
            int sp = 0; while (sp < line.Length && line[sp] == ' ') sp++;
            string r = line.Substring(sp);
            if (r.StartsWith("- ") || r.StartsWith("* ") || r.StartsWith("+ "))
            { indent = sp / 2; rest = r.Substring(2); return true; }
            indent = 0; rest = null; return false;
        }

        private static bool TryOrdered(string line, out int indent, out string marker, out string rest)
        {
            int sp = 0; while (sp < line.Length && line[sp] == ' ') sp++;
            int d = sp; while (d < line.Length && char.IsDigit(line[d])) d++;
            if (d > sp && d + 1 < line.Length && line[d] == '.' && line[d + 1] == ' ')
            { indent = sp / 2; marker = line.Substring(sp, d - sp); rest = line.Substring(d + 2); return true; }
            indent = 0; marker = null; rest = null; return false;
        }
    }
}
