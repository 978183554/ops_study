FROM golang:1.25
WORKDIR /app
COPY . .
RUN go mod download && go build -o myapp .
EXPOSE 8080

CMD ["./myapp"]