FROM golang:1.22-bullseye

RUN apt-get update && \
    apt-get install -y --no-install-recommends ruby-full build-essential && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

ENV BUNDLE_PATH=/workspace/.bundle

COPY Gemfile Gemfile.lock ./
RUN gem install bundler --no-document && \
    bundle config set --local path "$BUNDLE_PATH" && \
    bundle install --jobs 4 --retry 3

COPY go.mod go.sum ./
RUN go mod download

COPY . .

CMD ["bash", "experiments/run_experiment.sh"]
