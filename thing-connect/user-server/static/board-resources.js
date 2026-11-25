// Shared by the home page, board catalog and product detail pages.
(() => {
  function playerURL(raw) {
    let url;
    try { url = new URL(raw); } catch { return ''; }
    if (url.protocol !== 'https:' || !['www.bilibili.com', 'm.bilibili.com', 'bilibili.com'].includes(url.host)) return '';
    const video = /^\/video\/(BV[0-9A-Za-z]{10}|av\d+)\/?$/.exec(url.pathname)?.[1];
    if (!video) return '';
    const player = new URL('https://player.bilibili.com/player.html');
    if (video.startsWith('BV')) player.searchParams.set('bvid', video);
    else player.searchParams.set('aid', video.slice(2));
    const page = url.searchParams.get('p');
    if (/^[1-9]\d*$/.test(page)) player.searchParams.set('page', page);
    player.searchParams.set('autoplay', '1');
    return player.href;
  }

  function enableVideo(container, raw, name) {
    container.querySelector('.board-video-play')?.remove();
    const src = playerURL(raw);
    if (!src) return;
    container.classList.add('board-video-art');
    const play = document.createElement('button');
    play.type = 'button';
    play.className = 'board-video-play';
    const label = () => (document.documentElement.lang === 'en' ? 'Play demo video' : '播放演示视频') + ' · ' + name;
    play.setAttribute('aria-label', label());
    play.addEventListener('click', () => {
      const dialog = document.createElement('dialog');
      dialog.className = 'board-video-dialog';
      dialog.setAttribute('aria-label', label());
      const close = document.createElement('button');
      close.type = 'button';
      close.textContent = '×';
      close.setAttribute('aria-label', document.documentElement.lang === 'en' ? 'Close' : '关闭');
      close.addEventListener('click', () => dialog.close());
      const frame = document.createElement('iframe');
      frame.title = label();
      frame.allow = 'autoplay; fullscreen; picture-in-picture';
      frame.allowFullscreen = true;
      frame.src = src;
      dialog.append(close, frame);
      dialog.addEventListener('click', event => { if (event.target === dialog) dialog.close(); });
      dialog.addEventListener('close', () => dialog.remove(), { once: true });
      document.body.append(dialog);
      dialog.showModal();
    });
    container.append(play);
  }

  function actions(board, t) {
    const group = document.createElement('div');
    group.className = 'catalog-actions';
    for (const [field, label, style] of [
      ['web_flasher_url', 'landing.flashNow', 'primary'],
      ['flashing_guide_url', 'landing.flashGuide', 'guide-link'],
      ['firmware_url', 'landing.latestFirmware', 'repo-link'],
      ['repository_url', 'landing.repository', 'repo-link'],
    ]) {
      if (!board[field]) continue;
      let url;
      try { url = new URL(board[field]); } catch { continue; }
      if (url.protocol !== 'https:' || url.username || url.password) continue;
      const link = document.createElement('a');
      link.className = style;
      link.href = url.href;
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
      link.textContent = t(label);
      if (style === 'repo-link') {
        let row = group.querySelector('.board-resource-links');
        if (!row) {
          row = document.createElement('span');
          row.className = 'board-resource-links';
          group.append(row);
        }
        row.append(link);
      } else group.append(link);
    }
    return group;
  }

  window.BoardResources = { enableVideo, actions };
})();
