const sourceState = document.querySelector("#source-state");

async function loadHeaderStatus() {
  try {
    const response = await fetch("/api/status", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error(`Status request failed with ${response.status}`);
    const status = await response.json();
    sourceState.dataset.state = status.state;
    sourceState.querySelector("span:last-child").textContent = `${status.sources.healthy}/${status.sources.configured} sources healthy`;
  } catch {
    sourceState.dataset.state = "degraded";
    sourceState.querySelector("span:last-child").textContent = "Pipeline state unavailable";
  }
}

loadHeaderStatus();
