
var loc = window.location, ws_uri;
if (loc.protocol === "https:") {
    ws_uri = "wss:";
} else {
    ws_uri = "ws:";
}
ws_uri += "//" + loc.host;
var path = loc.pathname.replace(/\/$/, '');
ws_uri += path + "/websocket";

var progTimer = null;
var progLast = null;

$(function(){
	var ws = new WebSocket(ws_uri);

	const url = new URL(window.location);
	const searchParams = new URLSearchParams(url.search);
	var inputURL = searchParams.get('url');
	if (inputURL) {
		$("#url").val(inputURL);
	}

	$("#url").on('paste', function(e) {
		var data = e.originalEvent.clipboardData.getData('Text');
		searchParams.set('url', encodeURI(data));
		console.log(searchParams.toString());
		window.location.search = searchParams;
	});

	$("#go-button").click(function() {
		if (ws.readyState === 1) {
			$("#spinner").show();
			$("#input-form").hide();
			$("#progress-bar > span").css("width", "0%")
				.text("0%");
			var url = $("#url").val();
			var val = {URL: url};
			ws.send(JSON.stringify(val));
			$("#status").append("Requesting URL " + url + "\n");
			$(this).prop('disabled', true);
		} else {
			$("#status").append("socket not ready\n")
		}
	});

	$("#searchbox span").click(function() {
		$("#url").val('');
	});


	/*
	ws.onopen = function() {
	};
	*/

	ws.onmessage = function (e)	{
		var msg = JSON.parse(e.data);
		if( 'Key' in msg ) {
			switch (msg.Key) {
				case 'error':
					$("#output").show();
					$("#spinner").hide();
					$("#status").append("Error: " + msg.Value + "\n");
					break;
				case 'unknown':
					$("#output").show();
					$("#status").prepend(msg.Value);
					break;
				case 'progress':
					$("#output").show();
					$("#spinner").hide();
					$("#progress-bar").show();
					progLast = Date.now();
					if(!progTimer) {
						progTimer = setInterval(() => {
							let now = Date.now();
							if( (now - progLast) > 4000) {
								$("#spinner").show();
								$("#progress-bar").hide();
							}
						},1000);
					}
					var pct = parseFloat(msg.Value.Pct);
					$("#progress-bar > span").css("width", pct + "%")
						.text(pct + "%");
					$("#eta").text( msg.Value.ETA );
					if( msg.Value.FileSize !== '' ) {
						$("#filesize").text( msg.Value.FileSize + " MB" );
					}
					break;
				case 'info':
					$("#output").show();
					$("#title").text( msg.Value.Title );
					var bytes = parseFloat(msg.Value.FileSize)
					$("#filesize").text( (bytes / 1024 / 1024).toFixed(2) + " MB" );
					break;
				case 'link':
					$("#output").show();
					clearTimeout(progTimer);
					$("#spinner").hide();
					var $link = $("<a>")
						.attr("href", encodeURI(msg.Value.DownloadURL))
						.attr("download", "")
						.text(msg.Value.DownloadURL);
					$("#links").append($link);
					$("#links").append("<br>");
					$("#progress-bar > span").css("width", "100%")
						.text("100%");
					$("#go-button").prop('disabled', false);
					break;
				case 'link_stream':
					console.log('link_stream', msg);
					$("#output").show();
					$("#playa").show();
					var $audio = $("#playa audio")
					//	.attr("autoplay", "")
						.attr("controls", "");
					var $source = $("<source>")
						.attr("src", encodeURI(msg.Value.DownloadURL))
						.attr("type", "audio/ogg")
						.text(msg.Value.DownloadURL);
					$audio.append($source);
					$audio.focus();
					break;
				case 'recent':
					if( msg.Value.length == 0 ) {
						break;
					}
					$("#recent").show();
					$("#recent_urls").empty();
					for (let i=0; i< msg.Value.length; i++) {
						var $link = $("<a>")
							.attr("href", encodeURI(msg.Value[i].URL))
							.attr("download", "")
							.text(msg.Value[i].URL);
							//.text(msg.Value[i].URL + " " + new Date(msg.Value[i].Timestamp).toString());
						$("#recent_urls").append($link);
						$("#recent_urls").append("<br>");
					}
					break;
			}
		}
	};

	ws.onclose = function()	{
		$("#status").append("Connection closed\n");
		console.log("Connection closed");
		$("#ws-status-light").toggleClass("on off");
	};

	ws.onopen = function(e) {
		$("#status").append("Connection opened\n");
		console.log("Connection opened");
		$("#ws-status-light").toggleClass("off on");
	}

	$("#togglePlay").click(function() {
		audio = $("#playa audio")[0];
		if (audio.paused) {
			console.log("paused");	
			audio.play();
		} else {
			console.log("playing");	
			audio.pause();
		}
	});
	$("#backAudio").click(function() {
		audio = $("#playa audio")[0];
		console.log("backaudio", audio.currentTime, audio.duration);

		if(audio.currentTime - 15.0 < 0) {
			audio.currentTime = 0.0;
		} else {
			audio.currentTime -= 15.0;
		}
	});
	$("#forwardAudio").click(function() {
		audio = $("#playa audio")[0];
		console.log("forwardaudio", audio.currentTime, audio.duration);
		if( (audio.currentTime + 15.0) >= audio.duration) {
			audio.currentTime = audio.duration - 1.0;
		} else {
			audio.currentTime += 15.0;
		}
	});
	$("#slowAudio").click(function() {
		audio = $("#playa audio")[0];
		console.log("slowaudio", audio.playbackRate);
		if( (audio.playbackRate - 0.1) < 0 ) {
			audio.playbackRate = 0.0;
		} else {
			audio.playbackRate -= 0.1;
		}
		$("#audioSpeed").text(audio.playbackRate.toFixed(2) + "x");
	});
	$("#speedAudio").click(function() {
		audio = $("#playa audio")[0];
		console.log("speedaudio", audio.playbackRate);
		if( (audio.playbackRate + 0.1) >= 2.0 ) {
			audio.playbackRate = 2.0;
		} else {
			audio.playbackRate += 0.1;
		}
		$("#audioSpeed").text(audio.playbackRate.toFixed(2) + "x");
	});

});
