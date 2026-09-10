(() => {
  const fallbackImage = `data:image/svg+xml,${encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 320 220"><rect width="320" height="220" rx="20" fill="#e5f5f6"/><rect x="76" y="40" width="168" height="140" rx="14" fill="#2b929c"/><rect x="102" y="66" width="116" height="72" rx="6" fill="#142329"/><text x="160" y="112" text-anchor="middle" fill="#78d2dc" font-size="24" font-family="Arial">BOARD</text></svg>')}`;
  const statusNames = { ready: '已适配', adapting: '适配中', planned: '计划中' };
  const statusClass = { ready: 'ready', adapting: 'doing', planned: 'planned' };
  const featured = document.querySelector('#featured-list');
  const list = document.querySelector('#board-list');
  const dialog = document.querySelector('#detail-dialog');
  const nav = document.querySelector('.site-header .nav');
  const menuButton = document.querySelector('.menu-button');
  let boards = [];

  const text = (tag, className, value) => {
    const element = document.createElement(tag);
    if (className) element.className = className;
    element.textContent = value || '';
    return element;
  };
  const image = (board) => {
    const element = document.createElement('img');
    element.src = board.image_url || fallbackImage;
    element.alt = `${board.name} 产品图`;
    element.loading = 'lazy';
    element.addEventListener('error', () => { element.src = fallbackImage; }, { once: true });
    return element;
  };
  const status = (board) => text('span', `status ${statusClass[board.adaptation_status] || 'planned'}`, statusNames[board.adaptation_status] || '计划中');
  const detailButton = (board) => {
    const button = text('button', 'detail', '详情 →');
    button.type = 'button';
    button.addEventListener('click', () => openDetail(board));
    return button;
  };
  const interactive = (element, board) => {
    element.tabIndex = 0;
    element.setAttribute('role', 'button');
    element.addEventListener('click', (event) => { if (event.target.tagName !== 'BUTTON') openDetail(board); });
    element.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openDetail(board); }
    });
    return element;
  };
  const featuredCard = (board) => {
    const card = document.createElement('article');
    card.className = 'featured-card';
    card.append(image(board));
    const identity = document.createElement('div');
    identity.append(text('small', '', board.vendor), text('h2', '', board.name), text('code', '', board.model));
    card.append(identity, status(board), detailButton(board));
    return interactive(card, board);
  };
  const boardRow = (board) => {
    const row = document.createElement('article');
    row.className = 'board-row';
    const identity = document.createElement('div');
    identity.className = 'identity';
    identity.append(text('small', '', board.vendor), text('h2', '', board.name));
    row.append(identity, text('code', 'model', board.model), text('b', 'chip', board.chip), text('span', 'capabilities', (board.capabilities || []).join(' · ')), status(board), detailButton(board));
    return interactive(row, board);
  };
  const setLink = (id, url) => {
    const element = document.querySelector(id);
    element.hidden = !url;
    if (url) element.href = url;
  };
  function openDetail(board) {
    const detailImage = document.querySelector('#detail-image');
    detailImage.src = board.image_url || fallbackImage;
    detailImage.alt = `${board.name} 产品图`;
    detailImage.onerror = () => { detailImage.src = fallbackImage; };
    document.querySelector('#detail-vendor').textContent = board.vendor;
    document.querySelector('#detail-name').textContent = board.name;
    document.querySelector('#detail-summary').textContent = board.summary;
    document.querySelector('#detail-model').textContent = board.model;
    document.querySelector('#detail-chip').textContent = board.chip;
    document.querySelector('#detail-maker').textContent = board.vendor;
    document.querySelector('#detail-caps').textContent = (board.capabilities || []).join(' · ');
    setLink('#detail-video', board.effect_video_url);
    setLink('#detail-buy', board.purchase_url);
    setLink('#detail-repo', board.repository_url);
    setLink('#detail-firmware', board.firmware_url);
    setLink('#detail-guide', board.flashing_guide_url);
    document.querySelector('#merchant-note').hidden = !board.purchase_url;
    history.replaceState(null, '', `/boards#${encodeURIComponent(board.detail_slug)}`);
    dialog.showModal();
  }
  function render(data) {
    boards = data.boards || [];
    document.querySelector('#board-count').textContent = `${boards.length} 款`;
    document.querySelector('#empty').hidden = boards.length !== 0;
    featured.replaceChildren(...boards.slice(0, 20).map(featuredCard));
    const more = boards.slice(20);
    document.querySelector('#more-section').hidden = more.length === 0;
    list.replaceChildren(...more.map(boardRow));
    let requested = '';
    try { requested = decodeURIComponent(location.hash.slice(1)); } catch (_) { /* Ignore malformed links. */ }
    const selected = boards.find((board) => board.detail_slug === requested);
    if (selected) openDetail(selected);
  }
  async function load() {
    document.querySelector('#catalog-error').hidden = true;
    try {
      const response = await fetch('/v1/boards', { headers: { Accept: 'application/json' } });
      const body = await response.json();
      if (!response.ok || body.code !== 200) throw new Error(body.msg || '加载失败');
      render(body.data);
    } catch (_) {
      document.querySelector('#catalog-error').hidden = false;
    }
  }
  dialog.querySelector('.dialog-close').addEventListener('click', () => dialog.close());
  dialog.addEventListener('click', (event) => { if (event.target === dialog) dialog.close(); });
  dialog.addEventListener('close', () => history.replaceState(null, '', '/boards'));
  document.querySelector('#retry').addEventListener('click', load);
  menuButton.addEventListener('click', () => {
    const open = nav.classList.toggle('menu-open');
    menuButton.setAttribute('aria-expanded', String(open));
    menuButton.setAttribute('aria-label', open ? '关闭导航' : '打开导航');
  });
  document.querySelectorAll('.nav-links a').forEach((link) => link.addEventListener('click', () => {
    nav.classList.remove('menu-open');
    menuButton.setAttribute('aria-expanded', 'false');
    menuButton.setAttribute('aria-label', '打开导航');
  }));
  const loggedIn = Boolean(localStorage.getItem('token'));
  document.querySelectorAll('[data-auth-guest]').forEach((item) => { item.hidden = loggedIn; });
  document.querySelectorAll('[data-auth-user]').forEach((item) => { item.hidden = !loggedIn; });
  load();
})();
