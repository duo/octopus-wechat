FROM alpine:3.17.2 AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

RUN set -ex && \
	cd /build && \
	git clone https://github.com/tom-snow/docker-ComWechat.git dc && \
	wget -q "https://github.com/tom-snow/docker-ComWechat/releases/download/v0.2_wc3.7.0.30/Tencent.zip" -O dc/wine/Tencent.zip && \
	echo 'build done'

FROM zixia/wechat:3.3.0.115

ENV DISPLAY=:5 \
	VNCPASS=YourSafeVNCPassword

USER user
WORKDIR /home/user

EXPOSE 5905

RUN set ex && \
	sudo apt-get update && \
	sudo apt-get --no-install-recommends install dumb-init tigervnc-standalone-server tigervnc-common openbox wget -y

COPY --from=builder /build/dc/wine/simsun.ttc  /home/user/.wine/drive_c/windows/Fonts/simsun.ttc
COPY --from=builder /build/dc/wine/微信.lnk /home/user/.wine/drive_c/users/Public/Desktop/微信.lnk
COPY --from=builder /build/dc/wine/system.reg  /home/user/.wine/system.reg
COPY --from=builder /build/dc/wine/user.reg  /home/user/.wine/user.reg
COPY --from=builder /build/dc/wine/userdef.reg /home/user/.wine/userdef.reg

COPY --from=builder /build/dc/wine/Tencent.zip /Tencent.zip

COPY scripts/init-agent.sh /usr/bin/init-agent.sh
COPY scripts/run.py /usr/bin/run.py

RUN set -ex && \
	sudo chmod a+x /usr/bin/init-agent.sh && \
	sudo chmod a+x /usr/bin/run.py && \
	rm -rf "/home/user/.wine/drive_c/Program Files/Tencent/" && \
	unzip -q /Tencent.zip && \
	cp -rf wine/Tencent "/home/user/.wine/drive_c/Program Files/" && \
	sudo rm -rf wine Tencent.zip && \
	sudo apt-get autoremove -y && \
	sudo apt-get clean && \
	sudo rm -fr /tmp/* && \
	echo 'build done'

ENTRYPOINT ["/usr/bin/dumb-init"]
CMD ["/usr/bin/run.py"]
