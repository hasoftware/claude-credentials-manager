(() => {
  "use strict";

  const menuButton = document.getElementById("menu-button");
  const menu = document.getElementById("sidebar-menu");
  const compact = window.matchMedia("(max-width: 760px)");

  function setMenu(open) {
    menu.hidden = compact.matches && !open;
    menuButton.hidden = !compact.matches;
    menuButton.setAttribute("aria-expanded", String(!menu.hidden));
  }
  setMenu(false);
  compact.addEventListener("change", () => setMenu(false));
  menuButton.addEventListener("click", () => setMenu(menu.hidden));
  document.addEventListener("keydown", event => {
    if (event.key === "Escape" && compact.matches && !menu.hidden) {
      setMenu(false);
      menuButton.focus();
    }
  });

  const workspace = document.getElementById("workspace");
  document.querySelector(".skip-nav").addEventListener("click", event => {
    event.preventDefault();
    workspace.focus();
  });

  const list = workspace.querySelector(".section-tabs");
  const tabs = Array.from(list.querySelectorAll("a"));
  const panels = tabs.map(tab => document.getElementById(tab.hash.slice(1)));
  list.setAttribute("role", "tablist");
  tabs.forEach((tab, index) => {
    const panel = panels[index];
    tab.id = "tab-" + panel.id;
    tab.setAttribute("role", "tab");
    tab.setAttribute("aria-controls", panel.id);
    panel.setAttribute("role", "tabpanel");
    panel.setAttribute("aria-labelledby", tab.id);
    panel.tabIndex = 0;
  });

  function select(tab) {
    tabs.forEach((item, index) => {
      const selected = item === tab;
      item.setAttribute("aria-selected", String(selected));
      item.tabIndex = selected ? 0 : -1;
      panels[index].hidden = !selected;
    });
    // Reveal an off-screen label without scrolling the document.
    const bounds = list.getBoundingClientRect();
    const label = tab.getBoundingClientRect();
    if (label.left < bounds.left) list.scrollLeft += label.left - bounds.left;
    if (label.right > bounds.right) list.scrollLeft += label.right - bounds.right;
    list.dispatchEvent(new CustomEvent("sectionchange", {detail: tab.hash.slice(1), bubbles: true}));
  }

  function activate(tab) {
    if (location.hash !== tab.hash) history.pushState(null, "", tab.hash);
    select(tab);
  }

  list.addEventListener("click", event => {
    const tab = event.target.closest("a");
    if (!tab || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    activate(tab);
  });
  list.addEventListener("keydown", event => {
    const index = tabs.indexOf(event.target);
    if (index < 0) return;
    let next;
    switch (event.key) {
      case "ArrowLeft": next = (index + tabs.length - 1) % tabs.length; break;
      case "ArrowRight": next = (index + 1) % tabs.length; break;
      case "Home": next = 0; break;
      case "End": next = tabs.length - 1; break;
      case " ": next = index; break;
      default: return;
    }
    event.preventDefault();
    activate(tabs[next]);
    tabs[next].focus({preventScroll: true});
  });

  function followLocation() {
    select(tabs.find(tab => tab.hash === location.hash) || tabs[0]);
  }
  window.addEventListener("hashchange", followLocation);
  followLocation();
})();
