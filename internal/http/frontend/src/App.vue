<template>
  <Header />
  <RouterView />
  <Footer />
  <ThemeConfig />
</template>

<script>
import { RouterView } from "vue-router";
import Footer from "./components/Footer.vue";
import Header from "./components/Header.vue";
import ThemeConfig from "./components/ThemeConfig.vue";
import API from "@/services/API";
import { getWebsocketURI } from "@/services/util";

import { mapStores } from "pinia";
import { useUserStore, useSettingsStore } from "@/store";

export default {
  name: "App",
  components: {
    Header,
    Footer,
    ThemeConfig,
  },
  data() {
    return {
      // localStorage in Firefox is string-only
      dark: localStorage.dark === "true" ? true : false,
      refresh: null,
      socket: null,
    };
  },
  watch: {
    dark(_newValue) {
      // localStorage in Firefox is string-only
      localStorage.dark = this.dark ? "true" : "false";
    },
  },
  created() {
    this.socket = new WebSocket(getWebsocketURI() + "/health");
    this.mapSocketEvents();
  },
  mounted() {
    this.fetchData();
    this.refresh = setInterval(
      this.fetchData,
      this.settingsStore.refreshInterval
    );
  },
  unmounted() {
    clearInterval(this.refresh);
  },
  methods: {
    mapSocketEvents() {
      this.socket.addEventListener("open", (event) => {
        console.log("Connected to websocket");
        setInterval(() => {
          this.socket.send("PING");
        }, 500);
      });

      this.socket.addEventListener("close", (event) => {
        console.error("Disconnected from websocket");
        console.error("Sleeping for 1 second before reconnecting");
        setTimeout(() => {
          this.socket = new WebSocket(getWebsocketURI() + "/health");
          this.mapSocketEvents();
        }, 1000);
      });

      this.socket.addEventListener("error", (event) => {
        console.error("Error from websocket", event);
        this.socket.close();
        this.socket = new WebSocket(getWebsocketURI() + "/health");
        this.mapSocketEvents();
      });

      this.socket.addEventListener("message", (event) => {
        if (event.data === "PONG") {
          return;
        }
        console.log("Message from websocket", event.data);
      });
    },
    fetchData() {
      // GET /users/me
      API.get("/users/me")
        .then((res) => {
          this.userStore.id = res.data.id;
          this.userStore.callsign = res.data.callsign;
          this.userStore.username = res.data.username;
          this.userStore.admin = res.data.admin;
          this.userStore.created_at = res.data.created_at;
          this.userStore.loggedIn = true;
        })
        .catch((err) => {
          this.userStore.loggedIn = false;
        });
    },
  },
  computed: {
    ...mapStores(useUserStore, useSettingsStore),
  },
};
</script>

<style scoped></style>
