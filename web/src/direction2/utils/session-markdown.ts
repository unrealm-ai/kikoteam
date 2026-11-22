import MarkdownIt from 'markdown-it';

// Model/tool text is untrusted. No raw HTML, embedded media or relative/local links.
const markdown = new MarkdownIt({ html: false, breaks: true, linkify: false, typographer: false });
markdown.validateLink = href => /^https?:\/\//i.test(href) && !/[\u0000-\u0020\u007f]/.test(href);
markdown.renderer.rules.link_open = (tokens, index, options, _env, renderer) => {
  tokens[index]!.attrSet('target', '_blank');
  tokens[index]!.attrSet('rel', 'noopener noreferrer');
  tokens[index]!.attrSet('referrerpolicy', 'no-referrer');
  return renderer.renderToken(tokens, index, options);
};
markdown.renderer.rules.image = (tokens, index) => `<span class="markdown-image-label">[图片：${markdown.utils.escapeHtml(tokens[index]!.content || '未加载')}]</span>`;
export function renderSessionMarkdown(source: string): string { return markdown.render(source); }
