const elements = {
  search: document.querySelector("#search"),
  location: document.querySelector("#location-filter"),
  track: document.querySelector("#track-filter"),
  role: document.querySelector("#role-filter"),
  sort: document.querySelector("#sort-filter"),
  eligibleCount: document.querySelector("#eligible-count"),
  todayCount: document.querySelector("#today-count"),
  companyCount: document.querySelector("#company-count"),
  sourceState: document.querySelector("#source-state"),
  lastUpdated: document.querySelector("#last-updated"),
  showingCount: document.querySelector("#showing-count"),
  loading: document.querySelector("#loading-state"),
  error: document.querySelector("#error-state"),
  empty: document.querySelector("#empty-state"),
  list: document.querySelector("#job-list"),
  retry: document.querySelector("#retry-button"),
  clear: document.querySelector("#clear-button"),
  loadMore: document.querySelector("#load-more"),
  loadMoreWrap: document.querySelector("#load-more-wrap"),
  emptyTitle: document.querySelector("#empty-title"),
  emptyCopy: document.querySelector("#empty-copy"),
  companyPicker: document.querySelector("#company-picker"),
  companyPickerLabel: document.querySelector("#company-picker-label"),
  companyPickerCaption: document.querySelector("#company-picker-caption"),
  companyFilterSearch: document.querySelector("#company-filter-search"),
  companyOptions: document.querySelector("#company-options"),
  showAllCompanies: document.querySelector("#show-all-companies"),
  systemSummary: document.querySelector("#system-summary"),
  stateUpdated: document.querySelector("#state-updated"),
  coverageCount: document.querySelector("#coverage-count"),
  discoveryCount: document.querySelector("#discovery-count"),
  dedupeCount: document.querySelector("#dedupe-count"),
  telegramState: document.querySelector("#telegram-state"),
  telegramCaption: document.querySelector("#telegram-caption"),
  sourceAttention: document.querySelector("#source-attention"),
  failureList: document.querySelector("#failure-list"),
  sourceSearch: document.querySelector("#source-search"),
  sourceRosterSummary: document.querySelector("#source-roster-summary"),
  sourceRosterList: document.querySelector("#source-roster-list"),
  candidateTotal: document.querySelector("#candidate-total"),
  candidateDue: document.querySelector("#candidate-due"),
  discoveryUnhealthy: document.querySelector("#discovery-unhealthy"),
  identityCount: document.querySelector("#identity-count"),
  observationCount: document.querySelector("#observation-count"),
  mergeCount: document.querySelector("#merge-count"),
  deliveryStaged: document.querySelector("#delivery-staged"),
  deliveryPending: document.querySelector("#delivery-pending"),
  deliverySent: document.querySelector("#delivery-sent"),
  deliveryFailed: document.querySelector("#delivery-failed"),
  deliverySuppressed: document.querySelector("#delivery-suppressed"),
  runtimeMode: document.querySelector("#runtime-mode"),
  runtimeCycle: document.querySelector("#runtime-cycle"),
  runtimeResult: document.querySelector("#runtime-result"),
  statusError: document.querySelector("#status-error"),
  statusRetry: document.querySelector("#status-retry"),
};

const state = {
  limit: 50,
  feedController: null,
  statusController: null,
  debounce: null,
  feedCache: new Map(),
  statusCache: new Map(),
  monitoredSources: null,
  feedData: null,
  hiddenCompanies: loadHiddenCompanies(),
  hiddenJobs: new Set(),
};

const hiddenCompaniesKey = "radar-lite:hidden-companies:v1";
const numberFormat = new Intl.NumberFormat("en-US");
const compactNumber = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 });
const relativeTime = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
const dashboardViews = new Set(["jobs", "companies", "system"]);
const requestCacheTTL = 30_000;
const requestCacheLimit = 20;
const enhancedSelects = new Map();
let openSelectControl = null;

function activeDashboardView() {
  const pathView = window.location.pathname.replace(/^\/+|\/+$/g, "").toLowerCase();
  if (dashboardViews.has(pathView)) return pathView;
  const requested = window.location.hash.slice(1).toLowerCase();
  return dashboardViews.has(requested) ? requested : "jobs";
}

function activateDashboardView({ scrollToTop = true } = {}) {
  const requestedHashView = window.location.hash.slice(1).toLowerCase();
  if (window.location.pathname === "/" && dashboardViews.has(requestedHashView)) {
    window.history.replaceState(null, "", `/${requestedHashView}`);
  }
  const activeView = activeDashboardView();
  document.querySelectorAll("main > [data-view]").forEach((panel) => {
    panel.hidden = panel.dataset.view !== activeView;
  });
  document.querySelectorAll("[data-view-link]").forEach((link) => {
    if (link.dataset.viewLink === activeView) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
  });
  document.body.dataset.view = activeView;
  if (scrollToTop && (dashboardViews.has(requestedHashView) || activeView !== "jobs")) {
    window.requestAnimationFrame(() => window.scrollTo({ top: 0, left: 0, behavior: "auto" }));
  }
}

function navigateDashboard(event) {
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  if (!(event.target instanceof Element)) return;
  const link = event.target.closest("a[href]");
  if (!link || link.target || link.hasAttribute("download")) return;
  const destination = new URL(link.href, window.location.href);
  const view = destination.pathname.replace(/^\/+|\/+$/g, "").toLowerCase();
  if (destination.origin !== window.location.origin || !dashboardViews.has(view)) return;
  event.preventDefault();
  if (`${window.location.pathname}${window.location.search}${window.location.hash}` !== `${destination.pathname}${destination.search}${destination.hash}`) {
    window.history.pushState(null, "", `${destination.pathname}${destination.search}${destination.hash}`);
  }
  activateDashboardView();
}

function readRequestCache(cache, key) {
  const entry = cache.get(key);
  if (!entry) return null;
  if (Date.now() - entry.storedAt >= requestCacheTTL) {
    cache.delete(key);
    return null;
  }
  return entry.data;
}

function writeRequestCache(cache, key, data) {
  cache.delete(key);
  cache.set(key, { data, storedAt: Date.now() });
  while (cache.size > requestCacheLimit) {
    cache.delete(cache.keys().next().value);
  }
}

function node(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function closeSelectControl(control, { restoreFocus = false } = {}) {
  if (!control) return;
  control.field.classList.remove("is-open");
  control.trigger.setAttribute("aria-expanded", "false");
  control.menu.hidden = true;
  if (openSelectControl === control) openSelectControl = null;
  if (restoreFocus) control.trigger.focus();
}

function openSelect(control, focusIndex) {
  if (openSelectControl && openSelectControl !== control) closeSelectControl(openSelectControl);
  control.field.classList.add("is-open");
  control.trigger.setAttribute("aria-expanded", "true");
  control.menu.hidden = false;
  openSelectControl = control;
  if (Number.isInteger(focusIndex)) {
    const option = control.options[Math.max(0, Math.min(focusIndex, control.options.length - 1))];
    option?.focus();
  }
}

function enhanceSelect(select) {
  const shell = select.closest(".select-shell");
  const field = select.closest(".select-field");
  const label = field?.querySelector(`label[for="${select.id}"]`);
  if (!shell || !field || !label) return;

  const trigger = node("button", "select-trigger");
  trigger.type = "button";
  trigger.id = `${select.id}-trigger`;
  trigger.setAttribute("aria-haspopup", "listbox");
  trigger.setAttribute("aria-expanded", "false");
  trigger.setAttribute("aria-labelledby", `${label.id} ${select.id}-value`);

  const value = node("span", "select-trigger-value");
  value.id = `${select.id}-value`;
  trigger.append(value);

  const menu = node("div", "select-menu");
  menu.id = `${select.id}-menu`;
  menu.setAttribute("role", "listbox");
  menu.setAttribute("aria-labelledby", label.id);
  menu.hidden = true;
  trigger.setAttribute("aria-controls", menu.id);

  const control = { select, field, trigger, value, menu, options: [] };
  select.hidden = true;
  [...select.options].forEach((sourceOption, index) => {
    const option = node("button", "select-option", sourceOption.textContent);
    option.type = "button";
    option.setAttribute("role", "option");
    option.dataset.value = sourceOption.value;
    option.tabIndex = -1;
    option.addEventListener("click", () => {
      const changed = select.value !== sourceOption.value;
      select.value = sourceOption.value;
      control.sync();
      closeSelectControl(control, { restoreFocus: true });
      if (changed) select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    menu.append(option);
    control.options.push(option);
  });

  control.sync = () => {
    const selectedIndex = Math.max(0, select.selectedIndex);
    value.textContent = select.options[selectedIndex]?.textContent || "Select";
    control.options.forEach((option, index) => {
      option.setAttribute("aria-selected", String(index === selectedIndex));
    });
  };

  trigger.addEventListener("click", () => {
    if (menu.hidden) openSelect(control);
    else closeSelectControl(control);
  });
  trigger.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !menu.hidden) {
      event.preventDefault();
      closeSelectControl(control);
      return;
    }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    let index = select.selectedIndex;
    if (event.key === "Home") index = 0;
    if (event.key === "End") index = control.options.length - 1;
    openSelect(control, index);
  });
  menu.addEventListener("keydown", (event) => {
    const activeIndex = control.options.indexOf(document.activeElement);
    if (event.key === "Escape") {
      event.preventDefault();
      closeSelectControl(control, { restoreFocus: true });
      return;
    }
    if (event.key === "Tab") {
      closeSelectControl(control);
      return;
    }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    let nextIndex = activeIndex < 0 ? select.selectedIndex : activeIndex;
    if (event.key === "ArrowDown") nextIndex = (nextIndex + 1) % control.options.length;
    if (event.key === "ArrowUp") nextIndex = (nextIndex - 1 + control.options.length) % control.options.length;
    if (event.key === "Home") nextIndex = 0;
    if (event.key === "End") nextIndex = control.options.length - 1;
    control.options[nextIndex]?.focus();
  });
  label.addEventListener("click", (event) => {
    event.preventDefault();
    trigger.focus();
  });
  field.addEventListener("focusout", (event) => {
    if (!field.contains(event.relatedTarget)) closeSelectControl(control);
  });

  shell.append(trigger, menu);
  field.classList.add("is-enhanced");
  control.sync();
  enhancedSelects.set(select, control);
}

function syncEnhancedSelects() {
  enhancedSelects.forEach((control) => control.sync());
}

function companyKey(company) {
  return String(company || "").trim().toLocaleLowerCase();
}

function loadHiddenCompanies() {
  try {
    const companies = JSON.parse(window.localStorage.getItem("radar-lite:hidden-companies:v1") || "[]");
    return new Map(Array.isArray(companies)
      ? companies.filter((company) => typeof company === "string" && company.trim())
        .map((company) => [companyKey(company), company.trim()])
      : []);
  } catch {
    return new Map();
  }
}

function persistHiddenCompanies() {
  try {
    window.localStorage.setItem(hiddenCompaniesKey, JSON.stringify([...state.hiddenCompanies.values()]));
  } catch {
    // The feed remains usable when storage is unavailable or blocked.
  }
}

function isCompanyHidden(company) {
  return state.hiddenCompanies.has(companyKey(company));
}

function jobKey(job) {
  return String(job?.id || job?.apply_url || "").trim();
}

function isJobHidden(job) {
  const key = jobKey(job);
  return key ? state.hiddenJobs.has(key) : false;
}

function hideJob(job) {
  const key = jobKey(job);
  if (!key) return;
  state.hiddenJobs.add(key);
  renderCurrentFeed();
}

function setCompanyVisible(company, visible) {
  const key = companyKey(company);
  if (!key) return;
  if (visible) state.hiddenCompanies.delete(key);
  else state.hiddenCompanies.set(key, String(company).trim());
  persistHiddenCompanies();
  renderCurrentFeed();
}

function companyInitials(company) {
  const words = String(company || "?").trim().split(/\s+/).filter(Boolean);
  return words.slice(0, 2).map((word) => word[0]).join("").toUpperCase() || "?";
}

const companyIconDomains = new Map(Object.entries({
  adobe: "adobe.com",
  amazon: "amazon.com",
  "amazon web services": "aws.amazon.com",
  apple: "apple.com",
  cloudflare: "cloudflare.com",
  databricks: "databricks.com",
  figma: "figma.com",
  github: "github.com",
  google: "google.com",
  ibm: "ibm.com",
  meta: "meta.com",
  microsoft: "microsoft.com",
  netflix: "netflix.com",
  notion: "notion.so",
  nvidia: "nvidia.com",
  openai: "openai.com",
  oracle: "oracle.com",
  roblox: "roblox.com",
  salesforce: "salesforce.com",
  servicenow: "servicenow.com",
  snowflake: "snowflake.com",
  stripe: "stripe.com",
  tiktok: "tiktok.com",
  vercel: "vercel.com",
}));

function companyIconDomain(company, resolvedDomain = "") {
  const normalizedDomain = String(resolvedDomain || "").trim().toLocaleLowerCase();
  return normalizedDomain || companyIconDomains.get(companyKey(company)) || "";
}

function companyMark(company, compact = false, resolvedDomain = "") {
  const mark = node("span", compact ? "company-mark company-mark-compact" : "company-mark");
  mark.setAttribute("aria-hidden", "true");
  mark.append(node("span", "company-initials", companyInitials(company)));
  const domain = companyIconDomain(company, resolvedDomain);
  if (domain) {
    const image = document.createElement("img");
    image.alt = "";
    image.loading = "lazy";
    image.referrerPolicy = "no-referrer";
    image.src = `https://www.google.com/s2/favicons?domain=${encodeURIComponent(domain)}&sz=64`;
    image.addEventListener("load", () => mark.classList.add("has-logo"), { once: true });
    image.addEventListener("error", () => image.remove(), { once: true });
    mark.append(image);
  }
  return mark;
}

function formatRelative(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Recently";
  const seconds = Math.round((date.getTime() - Date.now()) / 1000);
  const absolute = Math.abs(seconds);
  if (absolute < 60) return "Just now";
  if (absolute < 3600) return relativeTime.format(Math.round(seconds / 60), "minute");
  if (absolute < 86400) return relativeTime.format(Math.round(seconds / 3600), "hour");
  if (absolute < 604800) return relativeTime.format(Math.round(seconds / 86400), "day");
  return date.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

function trackLabel(track) {
  return track === "internship" ? "Internship" : "New grad";
}

function applyLink(url, label = "Apply") {
  const link = node("a", "apply-link", label);
  link.href = url;
  link.target = "_blank";
  link.rel = "noopener noreferrer";
  return link;
}

function renderJob(job) {
  const row = node("article", "job-row");
  row.setAttribute("role", "listitem");

  const primary = node("div", "job-primary");
  primary.append(companyMark(job.company, false, job.logo_domain));
  const copy = node("div", "job-copy");
  const companyLine = node("div", "job-company-line");
  companyLine.append(node("span", "job-company", job.company));
  const hide = node("button", "hide-listing", "×");
  hide.type = "button";
  hide.title = `Hide this ${job.company} listing`;
  hide.setAttribute("aria-label", `Hide ${job.title} at ${job.company}`);
  hide.addEventListener("click", () => hideJob(job));
  companyLine.append(hide);
  copy.append(companyLine);
  copy.append(node("span", "job-title", job.title));
  if (job.opening_count > 1) {
    copy.append(node("span", "opening-count", `${job.opening_count} distinct openings grouped`));
  }
  primary.append(copy);
  row.append(primary);

  row.append(node("div", "job-location", job.location || "Location not stated"));
  const track = node("div", "job-track");
  track.append(node("span", "track-label", trackLabel(job.track)));
  row.append(track);
  row.append(node("time", "job-time", formatRelative(job.first_seen_at)));

  const actions = node("div", "job-actions");
  if (job.opening_count > 1) {
    const toggle = node("button", "opening-toggle", `${job.opening_count} links`);
    toggle.type = "button";
    toggle.setAttribute("aria-expanded", "false");
    toggle.setAttribute("aria-controls", `openings-${job.id}`);
    actions.append(toggle);

    const links = node("div", "opening-links");
    links.id = `openings-${job.id}`;
    links.hidden = true;
    job.openings.forEach((opening, index) => {
      if (!opening.apply_url) return;
      const link = applyLink(opening.apply_url, `Opening ${index + 1}`);
      link.className = "opening-link";
      links.append(link);
    });
    toggle.addEventListener("click", () => {
      const expanded = toggle.getAttribute("aria-expanded") === "true";
      toggle.setAttribute("aria-expanded", String(!expanded));
      links.hidden = expanded;
      toggle.textContent = expanded ? `${job.opening_count} links` : "Hide links";
    });
    row.append(actions);
    row.append(links);
  } else {
    if (job.apply_url) {
      actions.append(applyLink(job.apply_url));
    } else {
      actions.append(node("span", "job-time", "Link unavailable"));
    }
    row.append(actions);
  }
  return row;
}

function renderSummary(summary) {
  elements.eligibleCount.textContent = numberFormat.format(summary.grouped_roles);
  elements.todayCount.textContent = numberFormat.format(summary.added_today);
  elements.companyCount.textContent = numberFormat.format(summary.companies);
  elements.lastUpdated.textContent = summary.last_updated_at
    ? `Source snapshot updated ${formatRelative(summary.last_updated_at)}`
    : "Waiting for the first source snapshot";
}

function feedCompanies(data) {
  const companies = new Map(state.hiddenCompanies);
  data.jobs.forEach((job) => companies.set(companyKey(job.company), job.company));
  return [...companies.values()].sort((left, right) => left.localeCompare(right));
}

function renderCompanyPicker(data) {
  const query = elements.companyFilterSearch.value.trim().toLocaleLowerCase();
  const companies = feedCompanies(data);
  const matches = companies.filter((company) => !query || company.toLocaleLowerCase().includes(query));
  const hiddenCount = state.hiddenCompanies.size;
  elements.companyPickerLabel.textContent = hiddenCount === 0
    ? "All visible"
    : `${hiddenCount} hidden`;
  elements.companyPickerCaption.textContent = hiddenCount === 0
    ? "Checked companies stay in your feed. Changes are saved in this browser."
    : `${hiddenCount} compan${hiddenCount === 1 ? "y is" : "ies are"} hidden only in this browser.`;
  elements.showAllCompanies.disabled = hiddenCount === 0;
  elements.companyOptions.replaceChildren(...matches.map((company) => {
    const option = node("label", "company-option");
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = !isCompanyHidden(company);
    checkbox.setAttribute("aria-label", `Show ${company} in the feed`);
    checkbox.addEventListener("change", () => setCompanyVisible(company, checkbox.checked));
    const identity = node("span", "company-option-identity");
    const job = data.jobs.find((candidate) => companyKey(candidate.company) === companyKey(company));
    identity.append(companyMark(company, true, job?.logo_domain), node("span", "", company));
    const visibility = node("span", `company-option-state${checkbox.checked ? "" : " is-hidden"}`, checkbox.checked ? "Shown" : "Hidden");
    option.append(checkbox, identity, visibility);
    return option;
  }));
  if (matches.length === 0) {
    elements.companyOptions.append(node("p", "company-options-empty", "No company matches that search."));
  }
}

function renderCurrentFeed() {
  if (!state.feedData) return;
  const data = state.feedData;
  const visibleJobs = data.jobs.filter((job) => !isCompanyHidden(job.company) && !isJobHidden(job));
  const renderedJobs = visibleJobs.slice(0, state.limit);
  const hiddenInResult = data.jobs.length - visibleJobs.length;
  elements.list.replaceChildren(...renderedJobs.map(renderJob));
  renderSummary(data.summary);
  renderCompanyPicker(data);
  elements.showingCount.textContent = visibleJobs.length === 0
    ? "No matching roles"
    : `${numberFormat.format(renderedJobs.length)} of ${numberFormat.format(visibleJobs.length)} visible${hiddenInResult ? ` · ${numberFormat.format(hiddenInResult)} hidden` : ""}`;
  elements.emptyTitle.textContent = data.total > 0 && visibleJobs.length === 0
    ? "Every matching role is hidden."
    : "No roles match these filters.";
  elements.emptyCopy.textContent = data.total > 0 && visibleJobs.length === 0
    ? "Reload to restore dismissed listings, or show a hidden company from Companies."
    : "Clear the search or widen one filter to see more openings.";
  elements.empty.hidden = visibleJobs.length !== 0;
  elements.loadMoreWrap.hidden = renderedJobs.length >= visibleJobs.length;
  elements.loading.hidden = true;
  elements.error.hidden = true;
}

function renderFeed(data) {
  state.feedData = data;
  renderCurrentFeed();
}

function telegramPresentation(telegram) {
  switch (telegram.state) {
    case "enabled":
      return { label: "Publishing on", caption: "external delivery active" };
    case "locked":
      return telegram.credentials_present
        ? { label: "Locked", caption: "credentials configured, user gate off" }
        : { label: "Locked", caption: "external delivery is off" };
    case "credentials_missing":
      return { label: "Setup needed", caption: "bot credentials missing" };
    default:
      return { label: "Log only", caption: "external delivery is off" };
  }
}

function renderFailure(failure) {
  const row = node("div", "failure-row");
  const identity = node("div", "failure-identity");
  identity.append(node("strong", "", failure.company || failure.source_id));
  identity.append(node("span", "", failure.provider || failure.source_id));
  const diagnostic = node("div", "failure-diagnostic");
  diagnostic.append(node("span", "", failure.last_error));
  diagnostic.append(node("small", "", `${failure.consecutive_failures} consecutive failures, checked ${formatRelative(failure.last_attempt_at)}`));
  row.append(identity, diagnostic);
  return row;
}

function renderSourceRoster() {
  const query = elements.sourceSearch.value.trim().toLowerCase();
  elements.sourceRosterList.classList.remove("source-roster-loading");
  elements.sourceRosterList.setAttribute("role", "list");
  elements.sourceRosterList.setAttribute("aria-busy", "false");
  elements.sourceRosterList.removeAttribute("aria-label");
  elements.sourceSearch.disabled = !Array.isArray(state.monitoredSources);
  if (!Array.isArray(state.monitoredSources)) {
    elements.sourceRosterSummary.textContent = "The source roster is unavailable from this server version.";
    elements.sourceRosterList.replaceChildren(node("p", "source-roster-empty", "Refresh after the server update completes."));
    return;
  }
  const grouped = new Map();
  state.monitoredSources.forEach((source) => {
    const key = companyKey(source.company || source.source_id);
    if (!grouped.has(key)) {
      grouped.set(key, {
        company: source.company || source.source_id,
        logoDomain: source.logo_domain || "",
        sources: [],
      });
    }
    const company = grouped.get(key);
    company.sources.push(source);
    if (!company.logoDomain && source.logo_domain) company.logoDomain = source.logo_domain;
  });
  const companies = [...grouped.values()];
  const matches = companies.filter((company) => !query || [company.company, ...company.sources.flatMap((source) => [source.source_id, source.provider])]
    .some((value) => String(value || "").toLowerCase().includes(query)));
  elements.sourceRosterSummary.textContent = query
    ? `${numberFormat.format(matches.length)} matching ${matches.length === 1 ? "company" : "companies"}`
    : `${numberFormat.format(companies.length)} companies across ${numberFormat.format(state.monitoredSources.length)} source routes`;
  elements.sourceRosterList.replaceChildren(...matches.map((company) => {
    const states = new Set(company.sources.map((source) => source.state));
    const companyState = states.has("failure") ? "failure" : states.has("success") ? "success" : "pending";
    const providers = [...new Set(company.sources.map((source) => providerLabel(source.provider)).filter(Boolean))];
    const observedCount = company.sources.reduce((total, source) => total + Number(source.observed_count || 0), 0);
    const lastAttempt = company.sources.reduce((latest, source) => {
      const value = new Date(source.last_attempt_at || 0).getTime();
      return value > latest ? value : latest;
    }, 0);
    const card = node("article", "source-roster-card");
    card.setAttribute("role", "listitem");
    const heading = node("div", "source-roster-card-heading");
    const identity = node("div", "source-roster-identity");
    identity.append(companyMark(company.company, true, company.logoDomain));
    const copy = node("div", "source-roster-copy");
    copy.append(node("strong", "", company.company));
    copy.append(node("span", "", providers.length > 2 ? `${providers.length} providers` : providers.join(" + ") || "Career site"));
    identity.append(copy);
    const health = node("span", `source-roster-state source-roster-state-${companyState}`, companyState);
    heading.append(identity, health);
    const details = node("div", "source-roster-details");
    details.append(node("span", "source-roster-count", companyState === "pending"
      ? "Awaiting first check"
      : `${numberFormat.format(observedCount)} postings read`));
    if (lastAttempt > 0) details.append(node("span", "source-roster-checked", `Checked ${formatRelative(lastAttempt)}`));
    if (company.sources.length > 1) details.append(node("span", "source-roster-routes", `${company.sources.length} sources`));
    card.append(heading, details);
    return card;
  }));
  if (matches.length === 0) {
    elements.sourceRosterList.append(node("p", "source-roster-empty", "No monitored company matches that search."));
  }
}

function providerLabel(provider) {
  const normalized = String(provider || "").trim().toLowerCase();
  const labels = {
    amazon_jobs: "Amazon Jobs",
    apple_jobs: "Apple Jobs",
    bytedance_careers: "ByteDance Careers",
    google_careers: "Google Careers",
    meta_careers: "Meta Careers",
  };
  if (labels[normalized]) return labels[normalized];
  return normalized.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function renderStatus(data) {
  const sources = data.sources;
  const failures = Array.isArray(sources.failures) ? sources.failures : [];
  const telegram = telegramPresentation(data.telegram);
  state.monitoredSources = Array.isArray(sources.monitored) ? sources.monitored : null;
  renderSourceRoster();
  elements.coverageCount.textContent = `${sources.healthy}/${sources.configured}`;
  elements.discoveryCount.textContent = numberFormat.format(data.discovery.promoted_sources);
  elements.dedupeCount.textContent = compactNumber.format(data.dedupe.canonical_jobs);
  elements.telegramState.textContent = telegram.label;
  elements.telegramCaption.textContent = telegram.caption;
  elements.candidateTotal.textContent = numberFormat.format(data.discovery.candidates);
  elements.candidateDue.textContent = numberFormat.format(data.discovery.due);
  elements.discoveryUnhealthy.textContent = numberFormat.format(data.discovery.unhealthy_sources);
  elements.identityCount.textContent = numberFormat.format(data.dedupe.identity_aliases);
  elements.observationCount.textContent = numberFormat.format(data.dedupe.source_observations);
  elements.mergeCount.textContent = numberFormat.format(data.dedupe.multi_source_jobs);
  elements.deliveryStaged.textContent = numberFormat.format(data.deliveries.staged || 0);
  elements.deliveryPending.textContent = numberFormat.format(data.deliveries.pending + data.deliveries.claimed);
  elements.deliverySent.textContent = numberFormat.format(data.deliveries.sent);
  elements.deliveryFailed.textContent = numberFormat.format(data.deliveries.failed);
  elements.deliverySuppressed.textContent = numberFormat.format(data.deliveries.suppressed);
  elements.runtimeMode.textContent = data.runtime.crawler_embedded ? "Crawler + UI" : "Read-only UI";
  elements.runtimeCycle.textContent = data.runtime.cycle_running && data.runtime.active_since
    ? `Running · ${formatRelative(data.runtime.active_since)}`
    : data.runtime.last_cycle_at ? formatRelative(data.runtime.last_cycle_at) : "No completed cycle";
  elements.runtimeResult.textContent = data.runtime.cycle_stale
    ? "Overdue"
    : data.runtime.cycle_running
    ? "Running"
    : data.runtime.last_cycle_error ? "Failed"
      : data.runtime.degraded ? "Degraded"
        : data.runtime.last_cycle_state === "success" ? "Ready" : "Pending";
  elements.stateUpdated.textContent = `State read ${formatRelative(data.generated_at)}`;

  let systemSummary = `${sources.healthy} of ${sources.configured} monitored sources are healthy.`;
  let stateLabel = `${sources.healthy}/${sources.configured} sources healthy`;
  if (data.state === "degraded") {
    if (data.runtime.cycle_stale) {
      systemSummary += " The crawler owner is overdue; its database lease may remain held until that process completes or exits.";
    } else if (data.runtime.last_cycle_error) {
      systemSummary += " The last crawler cycle failed; durable source and delivery state remains available.";
    } else if (sources.failed > 0) {
      systemSummary += ` ${sources.failed} active source${sources.failed === 1 ? " needs" : "s need"} attention, without blocking healthy routes.`;
    } else if (data.discovery.unhealthy_sources > 0) {
      systemSummary += ` ${data.discovery.unhealthy_sources} rejected discovery route${data.discovery.unhealthy_sources === 1 ? " is" : "s are"} quarantined outside active monitoring.`;
    }
  } else if (data.state === "pending") {
    systemSummary += ` ${sources.pending} source${sources.pending === 1 ? " is" : "s are"} waiting for a first result.`;
  } else {
    systemSummary += " Delivery decisions and identity aliases are durable.";
  }
  elements.systemSummary.textContent = systemSummary;
  elements.sourceState.dataset.state = data.state;
  elements.sourceState.querySelector("span:last-child").textContent = stateLabel;
  elements.failureList.replaceChildren(...failures.map(renderFailure));
  elements.sourceAttention.hidden = failures.length === 0;
  elements.statusError.hidden = true;
}

function queryString() {
  const params = new URLSearchParams();
  const search = elements.search.value.trim();
  if (search) params.set("q", search);
  params.set("location", elements.location.value);
  params.set("track", elements.track.value);
  params.set("role", elements.role.value);
  params.set("sort", elements.sort.value);
  // Company visibility is browser-local, so fetch the bounded feed once and
  // paginate after applying local exclusions.
  params.set("limit", "500");
  return params.toString();
}

async function loadFeed({ force = false } = {}) {
  if (state.feedController) state.feedController.abort();
  const requestKey = queryString();
  const cached = force ? null : readRequestCache(state.feedCache, requestKey);
  if (cached) {
    renderFeed(cached);
    return;
  }
  state.feedController = new AbortController();
  elements.error.hidden = true;
  elements.empty.hidden = true;
  if (!elements.list.childElementCount) elements.loading.hidden = false;

  try {
    const response = await fetch(`/api/jobs?${requestKey}`, {
      signal: state.feedController.signal,
      headers: { Accept: "application/json" },
    });
    if (!response.ok) throw new Error(`Feed request failed with ${response.status}`);
    const data = await response.json();
    writeRequestCache(state.feedCache, requestKey, data);
    renderFeed(data);
  } catch (error) {
    if (error.name === "AbortError") return;
    elements.loading.hidden = true;
    elements.empty.hidden = true;
    elements.error.hidden = false;
    elements.showingCount.textContent = "Feed unavailable";
  }
}

async function loadStatus({ force = false } = {}) {
  if (state.statusController) state.statusController.abort();
  const cached = force ? null : readRequestCache(state.statusCache, "status");
  if (cached) {
    renderStatus(cached);
    return;
  }
  state.statusController = new AbortController();
  try {
    const response = await fetch("/api/status", {
      signal: state.statusController.signal,
      headers: { Accept: "application/json" },
    });
    if (!response.ok) throw new Error(`Status request failed with ${response.status}`);
    const data = await response.json();
    writeRequestCache(state.statusCache, "status", data);
    renderStatus(data);
  } catch (error) {
    if (error.name === "AbortError") return;
    state.monitoredSources = null;
    renderSourceRoster();
    elements.statusError.hidden = false;
    elements.systemSummary.textContent = "Durable pipeline state could not be read. The job feed remains independent.";
    elements.sourceState.dataset.state = "degraded";
    elements.sourceState.querySelector("span:last-child").textContent = "Pipeline state unavailable";
  }
}

function resetAndLoad() {
  state.limit = 50;
  loadFeed();
}

function scheduleSearch() {
  window.clearTimeout(state.debounce);
  state.debounce = window.setTimeout(resetAndLoad, 180);
}

function clearFilters() {
  elements.search.value = "";
  elements.location.value = "all";
  elements.track.value = "all";
  elements.role.value = "all";
  elements.sort.value = "recent";
  syncEnhancedSelects();
  resetAndLoad();
  elements.search.focus();
}

elements.search.addEventListener("input", scheduleSearch);
[elements.location, elements.track, elements.role, elements.sort].forEach((element) => {
  element.addEventListener("change", resetAndLoad);
});
elements.retry.addEventListener("click", () => loadFeed({ force: true }));
elements.clear.addEventListener("click", clearFilters);
elements.statusRetry.addEventListener("click", () => loadStatus({ force: true }));
elements.sourceSearch.addEventListener("input", renderSourceRoster);
elements.companyFilterSearch.addEventListener("input", () => {
  if (state.feedData) renderCompanyPicker(state.feedData);
});
elements.showAllCompanies.addEventListener("click", () => {
  state.hiddenCompanies.clear();
  persistHiddenCompanies();
  renderCurrentFeed();
});
elements.companyPicker.addEventListener("toggle", () => {
  if (!elements.companyPicker.open) return;
  elements.companyFilterSearch.value = "";
  if (state.feedData) renderCompanyPicker(state.feedData);
  window.requestAnimationFrame(() => elements.companyFilterSearch.focus());
});
document.addEventListener("click", (event) => {
  if (openSelectControl && !openSelectControl.field.contains(event.target)) {
    closeSelectControl(openSelectControl);
  }
  if (elements.companyPicker.open && !elements.companyPicker.contains(event.target)) {
    elements.companyPicker.open = false;
  }
});
elements.loadMore.addEventListener("click", () => {
  state.limit = Math.min(state.limit + 50, 500);
  renderCurrentFeed();
});
document.addEventListener("keydown", (event) => {
  const searchTarget = activeDashboardView() === "companies" ? elements.sourceSearch : elements.search;
  if (event.key === "/" && activeDashboardView() !== "system" && document.activeElement !== searchTarget) {
    event.preventDefault();
    searchTarget.focus();
  }
  if (event.key === "Escape" && document.activeElement === elements.search && elements.search.value) {
    elements.search.value = "";
    resetAndLoad();
  }
  if (event.key === "Escape" && document.activeElement === elements.sourceSearch && elements.sourceSearch.value) {
    elements.sourceSearch.value = "";
    renderSourceRoster();
  }
});

document.addEventListener("click", navigateDashboard);
window.addEventListener("hashchange", () => activateDashboardView());
window.addEventListener("popstate", () => activateDashboardView());
[elements.location, elements.track, elements.role, elements.sort].forEach(enhanceSelect);
activateDashboardView();
loadFeed();
loadStatus();
