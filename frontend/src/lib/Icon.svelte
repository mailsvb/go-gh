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
    <!-- two arrows chasing each other around a circle -->
    <path d="M2.5 8a5.5 5.5 0 0 1 9.6-3.6" />
    <path d="M12.5 1.8v2.9H9.6" />
    <path d="M13.5 8a5.5 5.5 0 0 1-9.6 3.6" />
    <path d="M3.5 14.2v-2.9h2.9" />
  {:else if name === "gear"}
    <!-- eight-tooth cog with a center hole -->
    <path
      d="M6.71 3.17 L7.07 1.37 L8.93 1.37 L9.29 3.17 L10.50 3.67 L12.03 2.65 L13.35 3.97 L12.33 5.50 L12.83 6.71 L14.63 7.07 L14.63 8.93 L12.83 9.29 L12.33 10.50 L13.35 12.03 L12.03 13.35 L10.50 12.33 L9.29 12.83 L8.93 14.63 L7.07 14.63 L6.71 12.83 L5.50 12.33 L3.97 13.35 L2.65 12.03 L3.67 10.50 L3.17 9.29 L1.37 8.93 L1.37 7.07 L3.17 6.71 L3.67 5.50 L2.65 3.97 L3.97 2.65 L5.50 3.67 Z"
    />
    <circle cx="8" cy="8" r="2.1" />
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
  {:else if name === "close"}
    <path d="M4 4l8 8M12 4l-8 8" />
  {:else if name === "bell-off"}
    <!-- bell with a slash -->
    <path d="M4.5 11.5V7.5a3.5 3.5 0 0 1 5.6-2.8M11.5 7.5v4M3 11.5h10M6.8 13.5h2.4" />
    <path d="M2.5 2.5l11 11" />
  {/if}
</svg>
