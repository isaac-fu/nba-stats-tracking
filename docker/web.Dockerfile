FROM node:24-bookworm-slim
WORKDIR /app
COPY web/build/client/package.json web/build/client/package-lock.json ./
RUN npm ci
COPY web/build/client/ ./
RUN chown -R node:node /app
USER node
EXPOSE 5173
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0", "--port", "5173", "--strictPort"]
