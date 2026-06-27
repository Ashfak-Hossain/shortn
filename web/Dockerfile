# ---- build stage: compile the React app to static files ----
# Heavy (Node + all deps), but it's discarded — only its dist/ output ships.
FROM node:22-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build          # Vite base is /app/, so assets resolve under /app/

# ---- serve stage: a tiny nginx that serves the built files ----
FROM nginx:1.27-alpine
# The dashboard is served under /app, so the files live under that path on disk.
COPY --from=build /app/dist /usr/share/nginx/html/app
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
