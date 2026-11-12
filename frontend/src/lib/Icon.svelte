<script lang="ts">
  // Small hand-drawn 16px icons (stroke based).
  let { name, size = 16 }: { name: string; size?: number } = $props();
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 16 16"
  fill="none"
  stroke="currentColor"
  stroke-width="1.5"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
>
  {#if name === "refresh"}
    <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" />
    <path d="M13.5 2.5v3h-3" />
  {:else if name === "gear"}
    <circle cx="8" cy="8" r="2.2" />
    <path
      d="M8 1.5v1.8M8 12.7v1.8M14.5 8h-1.8M3.3 8H1.5M12.6 3.4l-1.3 1.3M4.7 11.3l-1.3 1.3M12.6 12.6l-1.3-1.3M4.7 4.7 3.4 3.4"
    />
  {:else if name === "logout"}
    <path d="M6 2.5H3.5a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1H6" />
    <path d="M10 5l3 3-3 3M13 8H6" />
  {:else if name === "back"}
    <path d="M10 3 5 8l5 5" />
  {:else if name === "push"}
    <path d="M8 13.5v-11M4 6.5l4-4 4 4" />
  {:else if name === "tag"}
    <path d="M2.5 2.5h5l6 6-5 5-6-6z" />
    <circle cx="5.5" cy="5.5" r="0.8" />
  {:else if name === "branch"}
    <circle cx="4.5" cy="3.5" r="1.5" />
    <circle cx="4.5" cy="12.5" r="1.5" />
    <circle cx="11.5" cy="5" r="1.5" />
    <path d="M4.5 5v6M11.5 6.5c0 3-7 2-7 4.5" />
  {:else if name === "release"}
    <path d="M2.5 5 8 2.5 13.5 5v6L8 13.5 2.5 11z" />
    <path d="M2.5 5 8 7.5 13.5 5M8 7.5v6" />
  {:else if name === "lock"}
    <rect x="3.5" y="7" width="9" height="6.5" rx="1" />
    <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
  {:else if name === "external"}
    <path d="M9.5 2.5h4v4M13.5 2.5 7.5 8.5M12 9.5v3a1 1 0 0 1-1 1H3.5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1h3" />
  {/if}
</svg>
