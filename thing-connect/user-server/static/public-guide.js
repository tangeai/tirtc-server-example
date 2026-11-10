// Progressive enhancement: all content and navigation work without JavaScript.
(() => {
  const menu = document.querySelector('.guide-mobile');
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && menu?.open) {
      menu.open = false;
      menu.querySelector('summary').focus();
    }
  });
  document.addEventListener('click', (event) => {
    if (menu?.open && (!menu.contains(event.target) || event.target.closest('a'))) menu.open = false;
    const questionLink = event.target.closest('.faq-sidebar a[href^="#"]');
    if (questionLink) openAnswer(questionLink.getAttribute('href'));
  });
  function openAnswer(hash = location.hash) {
    let id;
    try { id = decodeURIComponent(hash.slice(1)); } catch { return; }
    const answer = document.getElementById(id);
    if (answer?.matches('details.faq-item')) answer.open = true;
  }
  window.addEventListener('hashchange', () => openAnswer());
  openAnswer();
})();
