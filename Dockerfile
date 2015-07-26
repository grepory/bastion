FROM kiasaki/alpine-golang

ENV BARTNET_HOST="api-beta.opsee.co:4080"
ENV NSQD_HOST="nsqd:4150"
ENV CA_PATH="ca.pem"
ENV CERT_PATH="cert.pem"
ENV KEY_PATH="key.pem"
ENV CUSTOMER_ID="unknown-customer"
ENV HOSTNAME=""
ENV AWS_ACCESS_KEY_ID=""
ENV AWS_SECRET_ACCESS_KEY=""

ADD target/linux/cmd/ /
